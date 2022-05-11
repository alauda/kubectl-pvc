package plugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	appv1 "github.com/alauda/helm-crds/pkg/apis/app/v1"
	"github.com/alauda/helm-crds/pkg/apis/app/v1alpha1"
	"github.com/alauda/helm-crds/pkg/apis/app/v1beta1"
	clientset "github.com/alauda/helm-crds/pkg/client/clientset/versioned"
	"github.com/alauda/helm-crds/pkg/client/clientset/versioned/scheme"
	"github.com/pkg/errors"
	"github.com/teris-io/shortid"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

// CaptainContext holds context for captain command
type CaptainContext struct {
	flags     *genericclioptions.ConfigFlags
	cli       clientset.Interface
	config    *rest.Config
	namespace string

	// core client to create event
	core kubernetes.Interface
}

func NewCaptainContext(streams genericclioptions.IOStreams) *CaptainContext {
	return &CaptainContext{
		flags: genericclioptions.NewConfigFlags(true),
	}
}

func (p *CaptainContext) Complete(namespace string) (err error) {
	p.namespace = namespace

	configLoader := p.flags.ToRawKubeConfigLoader()

	p.config, err = configLoader.ClientConfig()
	if err != nil {
		klog.Errorf("initial rest.Config obj config failed, err: %v", err)
		return err
	}

	p.cli, err = clientset.NewForConfig(p.config)
	if err != nil {
		klog.Errorf("initial kubernetes.clientset obj cli failed, err: %v", err)
		return err
	}

	p.core, err = kubernetes.NewForConfig(p.config)
	if err != nil {
		klog.Errorf("init kubernetes core client failed, err: %v", err)
		return err
	}

	return nil
}

func (p *CaptainContext) GetChartRepo(name, namespace string) (*v1beta1.ChartRepo, error) {
	return p.cli.AppV1beta1().ChartRepos(namespace).Get(name, metav1.GetOptions{})
}

func (p *CaptainContext) UpdateChartRepo(repo *v1beta1.ChartRepo) (*v1beta1.ChartRepo, error) {
	return p.cli.AppV1beta1().ChartRepos(p.namespace).Update(repo)
}

func (p *CaptainContext) PatchChartRepo(name string, data []byte) (result *v1beta1.ChartRepo, err error) {
	return p.cli.AppV1beta1().ChartRepos(p.namespace).Patch(name, types.MergePatchType, data)
}

// there should be only one deployed release for each helmrequest
func (p *CaptainContext) GetDeployedRelease(name, namespace string) (*appv1.Release, error) {
	opts := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("name=%s,status=deployed", name),
	}
	result, err := p.cli.AppV1().Releases(namespace).List(opts)
	if err != nil {
		return nil, err
	}

	if len(result.Items) < 1 {
		return nil, errors.New("cannot find deployed release")
	}

	return &result.Items[0], nil
}

func (p *CaptainContext) GetHelmRequest(name string) (*appv1.HelmRequest, error) {
	return p.cli.AppV1().HelmRequests(p.namespace).Get(name, metav1.GetOptions{})
}

func (p *CaptainContext) CreateHelmRequest(new *appv1.HelmRequest) (*appv1.HelmRequest, error) {
	return p.cli.AppV1().HelmRequests(new.GetNamespace()).Create(new)
}

func (p *CaptainContext) UpdateHelmRequest(new *appv1.HelmRequest) (*appv1.HelmRequest, error) {
	return p.cli.AppV1().HelmRequests(p.namespace).Update(new)
}

func (p *CaptainContext) UpdateHelmRequestStatus(new *appv1.HelmRequest) (*appv1.HelmRequest, error) {
	return p.cli.AppV1().HelmRequests(p.namespace).UpdateStatus(new)
}

func (p *CaptainContext) CreateChartRepo(new *v1alpha1.ChartRepo) (*v1alpha1.ChartRepo, error) {
	return p.cli.AppV1alpha1().ChartRepos(new.GetNamespace()).Create(new)
}

func (p *CaptainContext) GetNamespace() string {
	return p.namespace
}

func (p *CaptainContext) GetRestConfig() *rest.Config {
	return p.config
}

func (p *CaptainContext) GetConfigMap(name string) (*v1.ConfigMap, error) {
	return p.core.CoreV1().ConfigMaps(p.namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

func (p *CaptainContext) GetEventsMessage(hr *appv1.HelmRequest) (string, error) {
	events, err := p.core.CoreV1().Events(hr.Namespace).Search(scheme.Scheme, hr)
	if err != nil {
		return "", err
	}

	msg := ""
	for _, event := range events.Items {
		msg += event.Message + ","
	}

	return msg, err

}

// CreateEvent a event for upgrade/rollback...
func (p *CaptainContext) CreateEvent(et string, reason, message string, hr *appv1.HelmRequest) {

	uid, _ := shortid.Generate()

	event := v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: hr.Name + "." + strings.ToLower(uid),
		},
		Type: et,
		Source: v1.EventSource{
			Component: "kubectl-captain",
		},
		Reason:  reason,
		Message: message,
		InvolvedObject: v1.ObjectReference{
			// why it's not work use hr.Kind
			Kind:            "HelmRequest",
			Namespace:       hr.Namespace,
			Name:            hr.Name,
			UID:             hr.UID,
			APIVersion:      hr.APIVersion,
			ResourceVersion: hr.ResourceVersion,
		},
		LastTimestamp:  metav1.NewTime(time.Now()),
		FirstTimestamp: metav1.NewTime(time.Now()),
	}
	_, err := p.core.CoreV1().Events(hr.Namespace).Create(context.TODO(), &event, metav1.CreateOptions{})
	if err != nil {
		klog.Errorf("create event for helmrequest %s error: %s", hr.Name, err.Error())
	}
	return
}
