package app

import (
	"fmt"
	"strings"
	"time"

	appv1 "github.com/alauda/helm-crds/pkg/apis/app/v1"
	"github.com/alauda/kubectl-captain/pkg/plugin"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/cli/values"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

var (
	createExample = `
	# create helmrequest in default ns to set it's chart version to 1.5.0 and set value 'a=b'
	kubectl captain create foo --chart=stable/nginx-ingress -v 1.5.0 --set=a=b -f=values.yaml
`
)

type CreateOption struct {
	chart      string
	version    string
	values     []string
	valueFiles []string

	wait    bool
	timeout int

	cm string

	// source appv1.ChartSource
	sourceType      string
	sourceAddress   string
	sourceSecretRef string

	pctx *plugin.CaptainContext
}

func NewCreateOption() *CreateOption {
	return &CreateOption{}
}

func NewCreateCommand() *cobra.Command {
	opts := NewCreateOption()

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "create a helmrequest",
		Example: createExample,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Complete(pctx); err != nil {
				return err
			}

			if err := opts.Validate(); err != nil {
				return err
			}

			if err := opts.Run(args); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringArrayVarP(&opts.values, "set", "s", []string{}, "custom values")
	cmd.Flags().StringVarP(&opts.version, "version", "v", "", "the chart version you want to use ")
	cmd.Flags().BoolVarP(&opts.wait, "wait", "w", false, "wait for the helmrequest to be synced")
	cmd.Flags().IntVarP(&opts.timeout, "timeout", "t", 0, "timeout for the wait")
	cmd.Flags().StringVarP(&opts.chart, "chart", "c", "", "chart name, format: <repo>/<chart>")
	cmd.Flags().StringVarP(&opts.cm, "configmap", "", "", "configmap to obtain values from, it must contains a key called 'values.yaml'")
	cmd.Flags().StringVar(&opts.sourceType, "source-type", "", "chart source type, can be CHART / HTTP / OCI, default is CHART")
	cmd.Flags().StringArrayVarP(&opts.valueFiles, "values", "f", []string{}, "specify values in a YAML file or a URL (can specify multiple)")
	cmd.Flags().StringVar(&opts.sourceAddress, "source-address", "", "chart address. either the URL of the http(s) endpoint or repo of the oci artifact")
	cmd.Flags().StringVar(&opts.sourceSecretRef, "source-secret-ref", "", "secret name. the secret should contain accessKeyId (username) base64 encoded, and secretKey (password) also base64 encoded")
	return cmd
}

func (opts *CreateOption) Complete(pctx *plugin.CaptainContext) error {
	opts.pctx = pctx
	return nil
}

func (opts *CreateOption) Validate() error {
	return nil
}

// Run do the real update
// 1. save the old spec to annotation
// 2. update
func (opts *CreateOption) Run(args []string) (err error) {
	if opts.pctx == nil {
		klog.Errorf("UpgradeOption.ctx should not be nil")
		return fmt.Errorf("UpgradeOption.ctx should not be nil")
	}

	if len(args) == 0 {
		return fmt.Errorf("user should input helmrequest name to create")
	}

	name := args[0]
	pctx := opts.pctx
	var hr appv1.HelmRequest

	hr.Spec.Version = opts.version
	hr.Spec.Chart = opts.chart
	hr.Name = name
	hr.Namespace = pctx.GetNamespace()

	// check configmap first
	if opts.cm != "" {
		_, err := pctx.GetConfigMap(opts.cm)
		if err != nil {
			return errors.Wrap(err, "ref configmap not eixst")
		}

		optional := false

		hr.Spec.ValuesFrom = []appv1.ValuesFromSource{
			{
				ConfigMapKeyRef: &v1.ConfigMapKeySelector{
					LocalObjectReference: v1.LocalObjectReference{Name: opts.cm},
					Key:                  "values.yaml",
					Optional:             &optional,
				},
			},
		}
	}

	// merge values
	valueOpts := &values.Options{
		Values:     opts.values,
		ValueFiles: opts.valueFiles,
	}
	vals, err := valueOpts.MergeValues(nil)
	if err != nil {
		return errors.Wrap(err, "failed parsing values")
	}

	hr.Spec.Values = chartutil.Values(vals)
	hr.Spec.Namespace = hr.Namespace

	switch strings.ToLower(opts.sourceType) {
	case "oci":
		hr.Spec.Source = &appv1.ChartSource{
			OCI: &appv1.ChartSourceOCI{
				Repo:      opts.sourceAddress,
				SecretRef: opts.sourceSecretRef,
			},
		}
	case "http":
		hr.Spec.Source = &appv1.ChartSource{
			HTTP: &appv1.ChartSourceHTTP{
				URL:       opts.sourceAddress,
				SecretRef: opts.sourceSecretRef,
			},
		}
	default:
	}

	_, err = pctx.CreateHelmRequest(&hr)
	if !opts.wait {
		if err == nil {
			klog.Info("Create helmrequest: ", hr.GetName())
		}
		return err
	} else {
		if err != nil {
			return err
		}
	}

	klog.Info("Start wait for helmrequest to be synced")

	f := func() (done bool, err error) {
		result, err := pctx.GetHelmRequest(hr.GetName())
		if err != nil {
			return false, err
		}

		if result.Status.Phase == "Failed" {
			return false, errors.New("helmrequest failed, please check it's event to find out why")
		}

		return result.Status.Phase == "Synced", nil
	}

	if opts.timeout != 0 {
		return wait.Poll(1*time.Second, time.Duration(opts.timeout)*time.Second, f)
	} else {
		return wait.PollInfinite(1*time.Second, f)
	}

}
