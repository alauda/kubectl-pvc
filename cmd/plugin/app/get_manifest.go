package app

import (
	"fmt"
	"github.com/alauda/kubectl-captain/pkg/plugin"
	"github.com/spf13/cobra"
	"k8s.io/klog"
)

var (
	getManifestExample = `
	# get manifest of a helmrequest
	kubectl captain get-manifest foo -n default
	
	# redirect to manifest to a file
	kubectl captain get-manifest foo -n default > foo.yaml
`
)

type GetManifestOption struct {
	pctx *plugin.CaptainContext
}

func NewGetManifestOption() *GetManifestOption {
	return &GetManifestOption{}
}

func NewGetManifestCommand() *cobra.Command {
	opts := NewGetManifestOption()

	cmd := &cobra.Command{
		Use:     "get-manifest",
		Short:   "get chart manifest for a helmrequest",
		Example: getManifestExample,
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

	return cmd
}

func (opts *GetManifestOption) Complete(pctx *plugin.CaptainContext) error {
	opts.pctx = pctx
	return nil
}

func (opts *GetManifestOption) Validate() error {
	return nil
}

// Run rollback a helmrequest
func (opts *GetManifestOption) Run(args []string) (err error) {
	if opts.pctx == nil {
		klog.Errorf("GetManifestOption.ctx should not be nil")
		return fmt.Errorf("GetManifestOption.ctx should not be nil")
	}

	if len(args) == 0 {
		return fmt.Errorf("user should input a helmrequest name  to get manifest")
	}

	pctx := opts.pctx
	hr, err := pctx.GetHelmRequest(args[0])
	if err != nil {
		return err
	}

	name := hr.Spec.ReleaseName
	if name == "" {
		name = hr.Name
	}
	ns := hr.Spec.Namespace
	if ns == "" {
		ns = hr.Namespace
	}

	rel, err := pctx.GetDeployedRelease(name, ns)
	if err != nil {
		return err
	}

	decoded, err := plugin.DecodeRelease(rel)
	if err != nil {
		return err
	}

	fmt.Print(decoded.Manifest)

	return nil

}
