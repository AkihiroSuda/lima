package kubernetesservice

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

type Protocol string

const (
	// UDP/SCTP when lima port forwarding works on those protocols.

	TCP Protocol = "tcp"
)

type Entry struct {
	Protocol Protocol
	IP       net.IP
	Port     uint16
}

type ServiceWatcher struct {
	rwMutex         sync.RWMutex
	serviceInformer cache.SharedIndexInformer
}

func NewServiceWatcher() *ServiceWatcher {
	return &ServiceWatcher{}
}

func (s *ServiceWatcher) setServiceInformer(serviceInformer cache.SharedIndexInformer) {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()
	s.serviceInformer = serviceInformer
}

func (s *ServiceWatcher) getServiceInformer() cache.SharedIndexInformer {
	s.rwMutex.RLock()
	defer s.rwMutex.RUnlock()
	return s.serviceInformer
}

func (s *ServiceWatcher) Start() {
	logrus.Info("Monitoring kubernetes services")
	const retryInterval = 10 * time.Second
	const pollImmediately = false
	_ = wait.PollUntilContextCancel(context.TODO(), retryInterval, pollImmediately, func(ctx context.Context) (done bool, err error) {
		kubeClient, err := tryGetKubeClient()
		if err != nil {
			logrus.Tracef("failed to get kube client: %v, will retry in %v", err, retryInterval)
			return false, nil
		}

		informerFactory := informers.NewSharedInformerFactory(kubeClient, time.Hour)
		serviceInformer := informerFactory.Core().V1().Services().Informer()
		informerFactory.Start(ctx.Done())
		cache.WaitForCacheSync(ctx.Done(), serviceInformer.HasSynced)

		s.setServiceInformer(serviceInformer)
		return true, nil
	})
}

func tryGetKubeClient() (kubernetes.Interface, error) {
	candidateKubeConfigs := []string{
		"/etc/rancher/k3s/k3s.yaml",
		"/root/.kube/config",
	}

	for _, kubeconfig := range candidateKubeConfigs {
		_, err := os.Stat(kubeconfig)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, fmt.Errorf("stat kubeconfig %s failed: %w", kubeconfig, err)
		}

		restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("build kubeconfig from %s failed: %w", kubeconfig, err)
		}
		u, err := url.Parse(restConfig.Host)
		if err != nil {
			return nil, fmt.Errorf("parse kubeconfig host %s failed: %w", restConfig.Host, err)
		}
		if u.Hostname() != "127.0.0.1" { // might need to support IPv6
			// ensures the kubeconfig points to local k8s
			continue
		}

		kubeClient, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return nil, err
		}

		return kubeClient, nil
	}

	return nil, errors.New("no valid kubeconfig found")
}

func (s *ServiceWatcher) GetPorts() []Entry {
	serviceInformer := s.getServiceInformer()
	if serviceInformer == nil {
		return nil
	}

	var entries []Entry
	for _, obj := range serviceInformer.GetStore().List() {
		service := obj.(*corev1.Service)
		if service.Spec.Type != corev1.ServiceTypeNodePort &&
			service.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}

		for _, portEntry := range service.Spec.Ports {
			var port int32
			if service.Spec.Type == corev1.ServiceTypeNodePort {
				port = portEntry.NodePort
			} else if service.Spec.Type == corev1.ServiceTypeLoadBalancer {
				port = portEntry.Port
			}

			entries = append(entries, Entry{
				Protocol: Protocol(strings.ToLower(string(portEntry.Protocol))),
				IP:       net.ParseIP("0.0.0.0"),
				Port:     uint16(port),
			})
		}
	}

	return entries
}
