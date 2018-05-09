package main

import (
	"log"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	// from client go
	"flag"
	"os"
	"path/filepath"
	"time"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

func main() {

	var kubeconfig *string
	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()
	log.Printf(*kubeconfig)

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}
	// create the clientset
	client := kubernetes.NewForConfigOrDie(config)

	sharedInformer := informers.NewSharedInformerFactory(client, 2*time.Minute)
	dynamicQuotaController := NewDynamicQuotacontroller(client, sharedInformer.Core().V1().Namespaces())
	log.Printf("starting Informer")
	sharedInformer.Start(nil)
	log.Printf("starting controller")
	dynamicQuotaController.Run(nil)

}
func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
