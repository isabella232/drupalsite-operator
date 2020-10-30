/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	webservicescernchv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
)

// DrupalSiteRequestReconciler reconciles a DrupalSiteRequest object
type DrupalSiteRequestReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=webservices.cern.ch.webservices.cern.ch,resources=drupalsiterequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=webservices.cern.ch.webservices.cern.ch,resources=drupalsiterequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.openshift.io,resources=deploymentconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete


func (r *DrupalSiteRequestReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("drupalsiterequest", req.NamespacedName)

	// Fetch the DrupalSiteRequest instance
	drupalSiteRequest := &webservices.cern.chv1alpha1.DrupalSiteRequest{}
	err := r.Get(ctx, req.NamespacedName, drupalSiteRequest)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("DrupalSiteRequest resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get DrupalSiteRequest")
		return ctrl.Result{}, err
	}

	// Check if the deploymentConfig for MySQL already exists, if not create a new one
	found := &apps.openshift.iov1.DeploymentConfig{}
	err = r.Get(ctx, types.NamespacedName{Name: "drupal-mysql-" + drupalSiteRequest.Name, Namespace: drupalSiteRequest.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep := r.deploymentConfigForDrupalSiteRequestMySQL(drupalSiteRequest)
		log.Info("Creating a new DeploymentConfig", "DeploymentConfig.Namespace", dep.Namespace, "DeploymentConfig.Name", dep.Name)
		err = r.Create(ctx, dep)
		if err != nil {
			log.Error(err, "Failed to create new DeploymentConfig", "DeploymentConfig.Namespace", dep.Namespace, "DeploymentConfig.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// Deployment created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get DeploymentConfig for drupal-mysql")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *DrupalSiteRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webservicescernchv1alpha1.DrupalSiteRequest{}).
		Complete(r)
}

// Add watches for other resources controller.Watch check API Use filters with labels to pick up the resource
// Structure
	// Fetch the CR
	// Add clean up and delete https://gitlab.cern.ch:8443/paas-tools/operators/authz-operator/blob/24717af14e0792a416168eaea99dd4bbd9b83b9b/pkg/controller/applicationregistration/applicationregistration_controller.go#L117-118
	// Actions on initialization 
	// Actions on maintaining https://gitlab.cern.ch:8443/paas-tools/operators/authz-operator/blob/24717af14e0792a416168eaea99dd4bbd9b83b9b/pkg/controller/applicationregistration/applicationregistration_controller.go#L152 

// Status - creation complete
// Transient Error Reconcile Error