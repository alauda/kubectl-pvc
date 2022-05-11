package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appv1 "github.com/alauda/helm-crds/pkg/apis/app/v1"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/cli/values"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"

	"github.com/alauda/kubectl-captain/pkg/plugin"
	pkgutil "github.com/alauda/kubectl-captain/pkg/util"
)

var (
	updateExample = `
	# upgrade helmrequest in default ns to set it's chart version to 1.5.0 and set value 'a=b'
	kubectl captain upgrade foo -n default -v 1.5.0 --set=a=b -f=values.yaml
`
)

type UpgradeOption struct {
	version       string
	values        []string
	valueFiles    []string
	replaceValues bool

	wait    bool
	timeout int

	// maybe the user what to use a different repo
	repo string

	cm string

	// source appv1.ChartSource
	sourceType      string
	sourceAddress   string
	sourceSecretRef string

	pctx *plugin.CaptainContext
}

func NewUpdateOption() *UpgradeOption {
	return &UpgradeOption{}
}

func NewUpgradeCommand() *cobra.Command {
	opts := NewUpdateOption()

	cmd := &cobra.Command{
		Use:     "upgrade",
		Short:   "upgrade a helmrequest",
		Example: updateExample,
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
	cmd.Flags().StringVarP(&opts.repo, "repo", "r", "", "chartrepo for the chart")
	cmd.Flags().StringVarP(&opts.cm, "configmap", "", "", "configmap to obtain values from, it must contains a key called 'values.yaml'")
	cmd.Flags().StringArrayVarP(&opts.valueFiles, "values", "f", []string{}, "specify values in a YAML file or a URL (can specify multiple)")
	cmd.Flags().BoolVarP(&opts.replaceValues, "replace-values", "", false, "replace origin values, default is false")
	cmd.Flags().StringVar(&opts.sourceType, "source-type", "", "chart source type, can be CHART / HTTP / OCI, default is CHART")
	cmd.Flags().StringVar(&opts.sourceAddress, "source-address", "", "chart address. either the URL of the http(s) endpoint or repo of the oci artifact")
	cmd.Flags().StringVar(&opts.sourceSecretRef, "source-secret-ref", "", "secret name. the secret should contain accessKeyId (username) base64 encoded, and secretKey (password) also base64 encoded")
	return cmd
}

func (opts *UpgradeOption) Complete(pctx *plugin.CaptainContext) error {
	opts.pctx = pctx
	return nil
}

func (opts *UpgradeOption) Validate() error {
	return nil
}

// Run do the real update
// 1. save the old spec to annotation
// 2. update
func (opts *UpgradeOption) Run(args []string) (err error) {
	if opts.pctx == nil {
		klog.Errorf("UpgradeOption.ctx should not be nil")
		return fmt.Errorf("UpgradeOption.ctx should not be nil")
	}

	if len(args) == 0 {
		return fmt.Errorf("user should input helmrequest name to upgrade")
	}

	pctx := opts.pctx
	hr, err := pctx.GetHelmRequest(args[0])
	if err != nil {
		return err
	}

	// TODO: remove
	old, err := json.Marshal(hr.Spec)
	if err != nil {
		return err
	}

	if hr.Annotations == nil {
		hr.Annotations = make(map[string]string)
	}
	hr.Annotations["last-spec"] = string(old)
	hr.Annotations["kubectl-captain.resync"] = time.Now().String()

	if opts.version != "" {
		hr.Spec.Version = opts.version
	}

	if opts.repo != "" {
		splits := strings.Split(hr.Spec.Chart, "/")
		hr.Spec.Chart = opts.repo + "/" + splits[1]
	}

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
	if !opts.replaceValues {
		vals = pkgutil.MergeMaps(hr.Spec.Values.AsMap(), vals)
	}

	hr.Spec.Values = chartutil.Values(vals)

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

	_, err = pctx.UpdateHelmRequest(hr)
	if !opts.wait {
		return err
	}

	klog.Info("Start wait for helmrequest to be synced")

	// For some unknown reasons, the desired chart version may not be synced at this time. So this step
	// may fail for not found the target chart version. We don't want to report this error directly, as Captain
	// will retry in the background and it will succeed mostly. So we add this errCount to act as some mechanism.
	// This should consider a temporary solution.
	errCount := 0
	retryLimit := opts.timeout
	if retryLimit <= 0 {
		retryLimit = 75
	}

	f := func() (done bool, err error) {
		result, err := pctx.GetHelmRequest(hr.GetName())
		if err != nil {
			return false, err
		}

		if result.Status.Phase == "Failed" && errCount >= retryLimit-1 {
			msg, err := pctx.GetEventsMessage(hr)
			if err != nil {
				klog.Error("get events for hr error:", err.Error())
			} else {
				klog.Info("helmrequest failed, events are: ", msg)
			}
			return false, errors.New("helmrequest failed")
		}

		if result.Status.Phase == "Failed" {
			errCount += 1
			return false, nil
		}

		return result.Status.Phase == "Synced", nil
	}

	if opts.timeout != 0 {
		err = wait.Poll(1*time.Second, time.Duration(opts.timeout)*time.Second, f)
	} else {
		err = wait.PollInfinite(1*time.Second, f)
	}

	if errCount > 0 {
		klog.Warning("Retried failed helmrequest...")
	}

	if err != nil {
		message := fmt.Sprintf("Updated helmrequest %s error with version: %s values: %+v, err: %s", hr.Name, opts.version, opts.values, err.Error())
		pctx.CreateEvent("Warning", "FailedSync", message, hr)
	} else {
		message := fmt.Sprintf("Updated helmrequest %s with version: %s values: %+v", hr.Name, opts.version, opts.values)
		pctx.CreateEvent("Normal", "Synced", message, hr)
	}

	return err

}
