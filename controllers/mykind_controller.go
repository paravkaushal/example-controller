/*
Copyright 2022.

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
	mygroupv1beta1 "github.com/paravkaushal/example-controller/api/v1beta1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MyKindReconciler reconciles a MyKind object
type MyKindReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

var (
	deploymentOwnerKey = ".metadata.controller"
)

//+kubebuilder:rbac:groups=mygroup.paravkaushal.dev,resources=mykinds,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=mygroup.paravkaushal.dev,resources=mykinds/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=mygroup.paravkaushal.dev,resources=mykinds/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MyKind object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *MyKindReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// log := r.Log.WithValues("mykind", req.NamespacedName)
	// TODO(user): your logic here
	// log.Info("fetching MyKind resource")
	myKind := mygroupv1beta1.MyKind{}
	if err := r.Client.Get(ctx, req.NamespacedName, &myKind); err != nil {
		// log.Error(err, "failed to get MyKind resource")
		// Ignore NotFound errors as they will be retried automatically if the
		// resource is created in future.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if err := r.cleanupOwnedResources(ctx, &myKind); err != nil {
		// log.Error(err, "failed to clean up old Deployment resources for this MyKind")
		return ctrl.Result{}, err
	}
	// log = log.WithValues("deployment_name", myKind.Spec.DeploymentName)

	// log.Info("checking if an existing Deployment exists for this resource")
	deployment := apps.Deployment{}
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: myKind.Namespace, Name: myKind.Spec.DeploymentName}, &deployment)
	if apierrors.IsNotFound(err) {
		// log.Info("could not find existing Deployment for MyKind, creating one...")

		deployment = *buildDeployment(myKind)
		if err := r.Client.Create(ctx, &deployment); err != nil {
			// log.Error(err, "failed to create Deployment resource")
			return ctrl.Result{}, err
		}

		// r.Recorder.Eventf(&myKind, core.EventTypeNormal, "Created", "Created deployment %q", deployment.Name)
		// log.Info("created Deployment resource for MyKind")
		return ctrl.Result{}, nil
	}
	if err != nil {
		// log.Error(err, "failed to get deployment for MyKind Resource")
		return ctrl.Result{}, err
	}
	// log.Info("existing Deployment resource already exists for MyKind, checking replica count")

	expectedReplicas := int32(1)
	if myKind.Spec.Replicas != nil {
		expectedReplicas = *myKind.Spec.Replicas
	}
	if *&deployment.Spec.Replicas != &expectedReplicas {
		// log.Info("updating replica count", "old_count", *deployment.Spec.Replicas, "new_count", expectedReplicas)

		deployment.Spec.Replicas = &expectedReplicas
		if err := r.Client.Update(ctx, &deployment); err != nil {
			// log.Error(err, "failed to Deployment update replica count")
			return ctrl.Result{}, nil
		}
		// r.Recorder.Eventf(&myKind, core.EventTypeNormal, "Scaled", "Scaled deployment %q to %d replicas", deployment.Name, expectedReplicas)
		return ctrl.Result{}, nil
	}
	// log.Info("replica count upto date", "replica_count", *deployment.Spec.Replicas)

	// log.Info("updating MyKind resource status")
	myKind.Status.ReadyReplicas = deployment.Status.ReadyReplicas

	if r.Client.Status().Update(ctx, &myKind); err != nil {
		// log.Error(err, "failed to update MyKind status")
		return ctrl.Result{}, nil
	}
	// log.Info("resource status synced")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MyKindReconciler) SetupWithManager(mgr ctrl.Manager) error {

	indexerFunc := func(rawObj client.Object) []string {
		// grab the deployment object, extract the owner
		depl := rawObj.(*apps.Deployment)
		owner := metav1.GetControllerOf(depl)
		if owner == nil {
			return nil
		}
		// make sure it's a MyKind
		if owner.APIVersion != mygroupv1beta1.GroupVersion.String() || owner.Kind != "MyKind" {
			return nil
		}
		return []string{owner.Name}
	}
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &apps.Deployment{}, deploymentOwnerKey, indexerFunc); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&mygroupv1beta1.MyKind{}).
		Owns(&apps.Deployment{}).
		Complete(r)
}

func (r *MyKindReconciler) cleanupOwnedResources(ctx context.Context, myKind *mygroupv1beta1.MyKind) error {
	// log.Info("finding existing Deployments for MyKind resource")

	// List all deployment resources owned by this MyKind
	var deployments apps.DeploymentList
	if err := r.List(ctx, &deployments, client.InNamespace(myKind.Namespace), client.MatchingFields{deploymentOwnerKey: myKind.Name}); err != nil {
		return err
	}
	deleted := 0

	for _, depl := range deployments.Items {
		if depl.Name == myKind.Spec.DeploymentName {
			// If this deployment's name matches the one on the MyKind resource
			// then do not delete it.
			continue
		}
		if err := r.Client.Delete(ctx, &depl); err != nil {
			// log.Error(err, "failed to delete Deployment resource")
		}

		// r.Recorder.Eventf(myKind, core.EventTypeNormal, "Deleted", "Deleted deployment %q", depl.Name)
		deleted++
	}
	// log.Info("finished cleaning up old deployment resources", "numbers_deleted", deleted)
	return nil
}

func buildDeployment(myKind mygroupv1beta1.MyKind) *apps.Deployment {

	deployment := apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            myKind.Spec.DeploymentName,
			Namespace:       myKind.Namespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(&myKind, mygroupv1beta1.GroupVersion.WithKind("MyKind"))},
		},
		Spec: apps.DeploymentSpec{
			Replicas: myKind.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"deployment": myKind.Spec.DeploymentName,
				},
			},
			Template: core.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"deployment": myKind.Spec.DeploymentName,
					},
				},
				Spec: core.PodSpec{
					Containers: []core.Container{
						{
							Name:  "server",
							Image: "nginx:alpine",
						},
					},
				},
			},
		},
	}
	return &deployment
}
