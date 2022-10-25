package tests

import (
	"crypto/tls"
	"fmt"
	"strings"
	"testing"
	"time"
	"log"

	"github.com/gruntwork-io/terratest/modules/helm"
	http_helper "github.com/gruntwork-io/terratest/modules/http-helper"
	"github.com/gruntwork-io/terratest/modules/k8s"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Teardown resources after test execution: namespace deleted
func setupTest(t *testing.T) func(t *testing.T, options *k8s.KubectlOptions, namespaceName string) {
	log.Println("setup test: No specific actions")

	return func(t *testing.T, options *k8s.KubectlOptions, namespaceName string) {
		log.Println("teardown test")
		k8s.DeleteNamespace(t, options, namespaceName)
	}
}

// This test checks the dynamic compatibility test for the sonarqube standard and dce charts
// Specifically, we perform tests that:
// - check if the required (default) number of pods is running
// - check if all the running pods have the containersReady condition set to true
// - check if the sonarqube application is ready (apis are ready to serve new requests)
func TestSonarQubeChartDynamicCompatibility(t *testing.T) {
	
	// Input values for our charts' tests
	table := []struct {
		name     string
		chartName    string
		expectedPods	int
		values   map[string]string
	}{
		{"standard-chart", "sonarqube", 2, map[string]string{"tests.enabled": "false",
		//"image.repository": "sonarqube-arm64-v8", // DO NOT SET: This is only an experimental setting for M1 users 
		}},
		{"dce-chart", "sonarqube-dce", 6, map[string]string{"tests.enabled": "false",
		//"searchNodes.image.repository": "sonarqube-arm64-v8", // DO NOT SET: This is only an experimental setting for M1 users 
		//"ApplicationNodes.image.repository": "sonarqube-arm64-v8", // DO NOT SET: This is only an experimental setting for M1 users 
		"ApplicationNodes.jwtSecret": "dZ0EB0KxnF++nr5+4vfTCaun/eWbv6gOoXodiAMqcFo=",
		}}, 
	}


	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) { 

	teardownTest := setupTest(t)

	chartName := tc.chartName
	values := tc.values
	expectedPods := tc.expectedPods

	fmt.Printf("Value is %v", values["tests.enabled"])

	// Path to the helm chart we will test
	helmChartPath := "../charts/" + chartName

	// Setup the kubectl config and context. Here we choose to use the defaults, which is:
	// - HOME/.kube/config for the kubectl config file
	// - Current context of the kubectl config file
	existingKubectlOptions := k8s.NewKubectlOptions("", "", "default")

	// Define a new namespace
	namespaceName := chartName + "-dynamic-test"
	k8s.CreateNamespace(t, existingKubectlOptions, namespaceName)
	kubectlOptions := k8s.NewKubectlOptions("", "", namespaceName)

	// Setup the args
	options := &helm.Options{
		ValuesFiles:    []string{helmChartPath+"/values.yaml"},
		SetValues:      values,
		KubectlOptions: kubectlOptions,
	}

	// Run RenderTemplate to render the template and capture the output.
	output := helm.RenderTemplate(t, options, helmChartPath, chartName, []string{})
	
	// delete all resources after test execution
	defer teardownTest(t, kubectlOptions, namespaceName)

	// Now use kubectl to apply the rendered template
	k8s.KubectlApplyFromString(t, kubectlOptions, output)

	// check the number of pods before moving on
	pods := k8s.ListPods(t, kubectlOptions, v1.ListOptions{})
	for len(pods) < expectedPods {
		pods = k8s.ListPods(t, kubectlOptions, v1.ListOptions{})
		time.Sleep(5 * time.Second)
		fmt.Printf("Currently,  %v pods are running\n", len(pods))
	}

	for _, pod := range pods {
		fmt.Printf("Checking, %v\n", pod.Name)
		// wait until all pods have the condition "ContainersReady"
		WaitUntilPodContainersReady(t, kubectlOptions, pod.Name, 100, 30*time.Second)

	}

	// open a tunnel to the running sonarqube instance using a random port
	pod_name := k8s.ListPods(t, kubectlOptions, v1.ListOptions{LabelSelector: "app="+chartName+",release="+chartName })[0].Name
	fmt.Printf("Opening a tunnel to %v\n", pod_name)
	tunnel := k8s.NewTunnel(
		kubectlOptions, k8s.ResourceTypePod, pod_name, 0, 9000)
	defer tunnel.Close()
	tunnel.ForwardPort(t)

	// verify that we get back a 200 OK with the "status UP" message
	retries := 15
	sleep := 5 * time.Second
	endpoint := fmt.Sprintf("http://%s/api/system/status", tunnel.Endpoint())
	tlsConfig := tls.Config{} // empty TLS config
	http_helper.HttpGetWithRetryWithCustomValidation(
		t,
		endpoint,
		&tlsConfig,
		retries,
		sleep,
		func(statusCode int, body string) bool {
			isOk := statusCode == 200
			isUp := strings.Contains(body, "\"status\":\"UP\"")
			return isOk && isUp
		},
	)
		})
	}
}
