package controllers

import (
	"bytes"
	"fmt"
	"io"
	"os"

	core_v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

// getClientConfig first tries to get a config object which uses the service account kubernetes gives to pods,
// if it is called from a process running in a kubernetes environment.
// Otherwise, it tries to build config from a default kubeconfig filepath if it fails, it fallback to the default config.
// Once it get the config, it returns the same.
func getClientConfig() (*rest.Config, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		err1 := err
		kubeconfig := os.Getenv("KUBECONFIG")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			err = fmt.Errorf("InClusterConfig as well as BuildConfigFromFlags Failed. Error in InClusterConfig: %+v\nError in BuildConfigFromFlags: %+v", err1, err)
			return nil, err
		}
	}
	return config, nil
}

// getClientsetFromConfig takes REST config and Create a clientset based on that and return that clientset
func getClientsetFromConfig(config *rest.Config) (*kubernetes.Clientset, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		err = fmt.Errorf("failed creating clientset. Error: %+v", err)
		return nil, err
	}
	return clientset, nil
}

// getClientset first tries to get a config object which uses the service account kubernetes gives to pods,
// if it is called from a process running in a kubernetes environment.
// Otherwise, it tries to build config from a default kubeconfig filepath if it fails, it fallback to the default config.
// Once it get the config, it creates a new Clientset for the given config and returns the clientset.
func getClientset() (*kubernetes.Clientset, error) {
	config, err := getClientConfig()
	if err != nil {
		return nil, err
	}
	return getClientsetFromConfig(config)
}

// getRESTClient first tries to get a config object which uses the service account kubernetes gives to pods,
// if it is called from a process running in a kubernetes environment.
// Otherwise, it tries to build config from a default kubeconfig filepath if it fails, it fallback to the default config.
// Once it get the config, it
func getRESTClient() (*rest.RESTClient, error) {
	config, err := getClientConfig()
	if err != nil {
		return &rest.RESTClient{}, err
	}
	return rest.RESTClientFor(config)
}

// execToPodThroughAPI exec to the pod with the command specified non-interactively.
// :param string command: list of the str which specify the command.
// :param string pod_name: Pod name
// :param string namespace: namespace of the Pod.
// :param io.Reader stdin: Standerd Input if necessary, otherwise `nil`
// :return: string: Output of the command. (STDOUT)
//          string: Errors. (STDERR)
//           error: If any error has occurred otherwise `nil`
func execToPodThroughAPI(containerName, podName, namespace string, stdin io.Reader, command ...string) (stdout string, stderr string, err error) {
	config, err := getClientConfig()
	if err != nil {
		return "", "", err
	}

	clientset, err := getClientsetFromConfig(config)
	if err != nil {
		return "", "", err
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")
	scheme := runtime.NewScheme()
	if err := core_v1.AddToScheme(scheme); err != nil {
		return "", "", fmt.Errorf("error adding to scheme: %v", err)
	}

	parameterCodec := runtime.NewParameterCodec(scheme)
	req.VersionedParams(&core_v1.PodExecOptions{
		Command:   command,
		Container: containerName,
		Stdin:     stdin != nil,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, parameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("error while creating Executor: %v", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
		Tty:    false,
	})
	if err != nil {
		return "", "", fmt.Errorf("error in Stream: %v", err)
	}

	return stdoutBuf.String(), stderrBuf.String(), nil
}
