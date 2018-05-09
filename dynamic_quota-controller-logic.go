package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	apicorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource" // for resource quantity
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	informercorev1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	listercorev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// DynamicQuotaController our controller struct
type DynamicQuotaController struct {
	kclient               *kubernetes.Clientset
	namespaceLister       listercorev1.NamespaceLister
	namespaceListerSynced cache.InformerSynced
	queue                 workqueue.RateLimitingInterface
}

// NewDynamicQuotacontroller function to build our controller
func NewDynamicQuotacontroller(client *kubernetes.Clientset, namespaceInformer informercorev1.NamespaceInformer) *DynamicQuotaController {
	c := &DynamicQuotaController{
		kclient:               client,
		namespaceLister:       namespaceInformer.Lister(),
		namespaceListerSynced: namespaceInformer.Informer().HasSynced,
		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "namespace dynamic quota"),
	}

	namespaceInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				//namespaceObj := obj.(*v1.Namespace)
				//namespaceName := namespaceObj.Name
				//log.Printf(namespaceName)
				key, err := cache.MetaNamespaceKeyFunc(obj)
				if err == nil {
					c.queue.AddRateLimited(key)
				}
				log.Printf("namespace: %s added", key)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				log.Print("namespace updated")
			},
			DeleteFunc: func(obj interface{}) {
				log.Print("namespace deleted")
				key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
				if err == nil {
					c.queue.AddRateLimited(key)
				}
			},
		},
	)
	return c

}

// Run will start the controller
func (c *DynamicQuotaController) Run(stop <-chan struct{}) {
	var wg sync.WaitGroup

	defer func() {
		log.Printf("shutting down queue")
		c.queue.ShutDown()
		log.Printf("shutting down workers")
		wg.Wait()
		log.Printf("queue and workers are exited")
	}()

	log.Printf("waiting for cache sync")
	if !cache.WaitForCacheSync(stop, c.namespaceListerSynced) {
		log.Printf("timed out waiting for cache sync")
		return
	}
	log.Printf("namespace cache is synced")

	go func() {
		wait.Until(c.runWorker, time.Second, stop)
		wg.Done()
	}()
	// wait until we're told to stop
	log.Print("waiting for stop signal")
	<-stop
	log.Print("received stop signal")
}

func (c *DynamicQuotaController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *DynamicQuotaController) processNextWorkItem() bool {
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(key)

	err := c.processItem(key.(string))
	if err == nil {

		c.queue.Forget(key)
		return true
	}
	runtime.HandleError(fmt.Errorf("updatequota failed with: %v", err))
	c.queue.AddRateLimited(key)
	return true
}

func (c *DynamicQuotaController) processItem(key string) error {
	log.Printf("got key %s from worker", key)
	// get number of namespaces
	namespacesList, _ := c.kclient.CoreV1().Namespaces().List(metav1.ListOptions{})
	namespaceCount := len(namespacesList.Items)
	fmt.Println(namespaceCount)

	// get nodes and calculate capacity of the cluster
	nodesList, _ := c.kclient.CoreV1().Nodes().List(metav1.ListOptions{})
	var memoryClusterCapacity int64
	var cpuClusterCapacityMilli int64

	for _, node := range nodesList.Items {
		memoryClusterCapacity += node.Status.Capacity.Memory().Value()
		cpuClusterCapacityMilli += node.Status.Capacity.Cpu().MilliValue()
	}
	resourceQuotaMemory := memoryClusterCapacity / int64(namespaceCount)
	resourceQuotaCPUMilli := cpuClusterCapacityMilli / int64(namespaceCount)

	for _, ns := range namespacesList.Items {
		// don't limit these namespaces
		if (ns.Name == "default") || (ns.Name == "kube-public") || (ns.Name == "kube-system") {
			continue
		}
		log.Printf("editing resource quota for namespace: %s", ns.Name)
		// construct the new resource quota of the namespace
		resourceQuota := &apicorev1.ResourceQuota{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ResourceQuota",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("compute-resources"),
				Namespace: ns.Name,
			},
			Spec: apicorev1.ResourceQuotaSpec{
				Hard: apicorev1.ResourceList{
					apicorev1.ResourceRequestsCPU:    *resource.NewScaledQuantity(resourceQuotaCPUMilli, resource.Scale(-3)),
					apicorev1.ResourceRequestsMemory: *resource.NewQuantity(resourceQuotaMemory, resource.Format("BinarySI")),
					apicorev1.ResourceLimitsCPU:      *resource.NewScaledQuantity(resourceQuotaCPUMilli, resource.Scale(-3)),
					apicorev1.ResourceLimitsMemory:   *resource.NewQuantity(resourceQuotaMemory, resource.Format("BinarySI")),
				},
			},
		}

		var err error
		rqlist, _ := c.kclient.CoreV1().ResourceQuotas(ns.Name).List(metav1.ListOptions{})
		if len(rqlist.Items) == 0 {
			log.Printf("creating new resource quota")
			_, err = c.kclient.CoreV1().ResourceQuotas(ns.Name).Create(resourceQuota)
		} else {
			log.Printf("updating resource quota")
			_, err = c.kclient.CoreV1().ResourceQuotas(ns.Name).Update(resourceQuota)
		}
		if err != nil {
			log.Printf("%v", err)
		}

	}

	return nil
}
