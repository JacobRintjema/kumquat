/*
Copyright 2024.

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

package controller

import (
	"context"
	"fmt"
	"strings"

	kumquatTemplate "kumquat/template"

	mapset "github.com/deckarep/golang-set/v2"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/yaml"

	kumquatv1beta1 "kumquat/api/v1beta1"
	"kumquat/repository"
)

const templateFinalizer = "kumquat.guidewire.com/finalizer"

// containsString checks if a string is in a slice
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// removeString removes a string from a slice
func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// EnsureFinalizer adds a finalizer to the resource if not present
func (r *TemplateReconciler) EnsureFinalizer(template *kumquatv1beta1.Template) bool {
	if !containsString(template.GetFinalizers(), templateFinalizer) {
		template.SetFinalizers(append(template.GetFinalizers(), templateFinalizer))
		return true
	}
	return false
}

// RemoveFinalizer removes the finalizer from the resource
func (r *TemplateReconciler) RemoveFinalizer(template *kumquatv1beta1.Template) bool {
	if containsString(template.GetFinalizers(), templateFinalizer) {
		template.SetFinalizers(removeString(template.GetFinalizers(), templateFinalizer))
		return true
	}
	return false
}

// TemplateReconciler reconciles a Template object
type TemplateReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	WatchManager WatchManager
	K8sClient    K8sClient
	Repository   repository.Repository
}

func (r *TemplateReconciler) handleDeletion(
	ctx context.Context,
	log logr.Logger,
	template *kumquatv1beta1.Template,
) (ctrl.Result, error) {
	log.Info("template deleted", "name", template.Name)
	r.WatchManager.RemoveWatch(template.Name)

	err := deleteAssociatedResources(template, r.Repository, log, r.K8sClient)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.RemoveFinalizer(template) {
		err := r.Update(ctx, template)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}
func deleteAssociatedResources(
	template *kumquatv1beta1.Template,
	re repository.Repository,
	log logr.Logger,
	k8sClient K8sClient,
) error {
	template.GetObjectKind().SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "kumquat.guidewire.com",
		Version: "v1beta1",
		Kind:    "Template",
	})
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(template)
	if err != nil {
		log.Error(err, "failed to convert template to unstructured map")
		return err
	}

	resource, err := repository.MakeResource(objMap)
	if err != nil {
		log.Error(err, "unable to make resource from object")
		return err
	}
	t, err := kumquatTemplate.NewTemplate(resource)
	if err != nil {
		log.Error(err, "unable to create template from resource")
		return err
	}
	o, err := t.Evaluate(re)
	if err != nil {
		log.Error(err, "unable to evaluate template")
		return err
	}
	fmt.Println(o.Output)
	for i := 0; i < o.Output.ResourceCount(); i++ {
		out, err := o.Output.ResultString(i)
		if err != nil {
			log.Error(err, "unable to get result string")
			return err
		}
		fmt.Println(out)

		err = deleteResourceFromCluster(out, log, k8sClient)
		if err != nil {
			return err
		}
	}
	return nil
}
func deleteResourceFromCluster(out string, log logr.Logger, k8sClient K8sClient) error {
	jsonData, err := yaml.YAMLToJSON([]byte(out))
	if err != nil {
		log.Error(err, "unable to convert YAML to JSON")
		return err
	}
	unstructuredObj := &unstructured.Unstructured{}
	err = unstructuredObj.UnmarshalJSON(jsonData)
	if err != nil {
		log.Error(err, "unable to unmarshal JSON")
		return err
	}

	context := context.TODO()
	err = k8sClient.Delete(context,
		unstructuredObj.GetObjectKind().GroupVersionKind().Group,
		unstructuredObj.GetKind(),
		unstructuredObj.GetNamespace(),
		unstructuredObj.GetName())
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			log.Info("resource already deleted", "resource", unstructuredObj.GetName())
		} else {
			log.Error(err, "unable to delete resource")
			return err
		}
	}
	return nil
}

// +kubebuilder:rbac:groups=kumquat.guidewire.com,resources=templates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kumquat.guidewire.com,resources=templates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kumquat.guidewire.com,resources=templates/finalizers,verbs=update
// +kubebuilder:rbac:groups=*,resources=*,verbs=*

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Template object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.18.4/pkg/reconcile
func (r *TemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	template := &kumquatv1beta1.Template{}
	err := r.Get(ctx, req.NamespacedName, template)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !template.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, log, template)
	}

	if r.EnsureFinalizer(template) {
		err := r.Update(ctx, template)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	gvkList, err := extractGVKsFromQuery(template.Spec.Query, r.Repository, log, r.K8sClient)
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, gvk := range gvkList {
		err := addDataToDatabase(gvk.Group, gvk.Kind, log, r.K8sClient, r.Repository)
		if err != nil {
			log.Error(err, "unable to add data to database", "gvk", gvk)
		}
	}
	data, err := r.Repository.Query(template.Spec.Query)
	fmt.Println(template, "this is template")
	if err != nil {
		log.Error(err, "unable to query database", "query", template.Spec.Query)
		return ctrl.Result{}, err
	}
	fmt.Println(len(data.Results), "found in the database")

	err = applyTemplateResources(template, r.Repository, log, r.K8sClient, r.WatchManager)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.WatchManager.UpdateWatch(template.Name, gvkList)
	if err != nil {
		log.Error(err, "unable to update watch for resource", "template", template.Name)
	}

	return ctrl.Result{}, nil
}

func extractGVKsFromQuery(
	query string,
	re repository.Repository,
	log logr.Logger,
	k8sClient K8sClient,
) ([]schema.GroupVersionKind, error) {
	tableNames := re.ExtractTableNamesFromQuery(query)
	gvkList := make([]schema.GroupVersionKind, 0, len(tableNames))

	for _, tableName := range tableNames {
		gvk, err := BuildTableGVK(tableName, log, k8sClient)
		if err != nil {
			log.Error(err, "unable to build GVK for table", "table", tableName)
			return nil, err
		}
		gvkList = append(gvkList, gvk)
	}
	return gvkList, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	c, err := controller.New("template-controller", mgr,
		controller.Options{
			Reconciler:         r,
			SkipNameValidation: ptr.To(true),
		})
	if err != nil {
		return err
	}

	err = c.Watch(source.Kind(
		mgr.GetCache(),
		&kumquatv1beta1.Template{},
		&handler.TypedEnqueueRequestForObject[*kumquatv1beta1.Template]{}))
	if err != nil {
		return err
	}

	r.WatchManager = NewWatchManager(mgr, r.K8sClient, r.Repository)

	return nil
}
func BuildTableGVK(tableName string, log logr.Logger, k8sClient K8sClient) (schema.GroupVersionKind, error) {
	dotIndex := strings.Index(tableName, ".")
	if dotIndex == -1 {
		return schema.GroupVersionKind{}, fmt.Errorf("invalid table name format")
	}

	kind := tableName[:dotIndex]
	group := tableName[dotIndex+1:]

	// The core API group is represented by the empty string in Kubernetes API calls
	if group == "core" {
		group = ""
	}

	gvk, err := k8sClient.GetPreferredGVK(group, kind)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}
	return gvk, nil
}

func addDataToDatabase(group string, kind string, log logr.Logger, k8sClient K8sClient, repo repository.Repository) error {
	fmt.Println("Adding data to database for", group, kind)

	context := context.TODO()

	data, err := k8sClient.List(context, group, kind, "")
	if err != nil {
		return err
	}
	log.Info("found in the cluster", "count", len(data.Items))

	for _, item := range data.Items {
		err := UpsertResourceToDatabase(repo, &item, context)
		if err != nil {
			return err
		}
	}
	return nil
}

func GetTemplateResourceFromCluster(kind string, group string, name string, log logr.Logger,
	k8sClient K8sClient) (*unstructured.Unstructured, error) {

	context := context.TODO()
	data, error := k8sClient.Get(context, group, kind, "", name)
	if error != nil {
		return &unstructured.Unstructured{}, error
	}
	return data, nil

}

var ProcessTemplateResources = processTemplateResources

// applyTemplateResources applies the resources generated from the template.
func applyTemplateResources(
	template *kumquatv1beta1.Template, re repository.Repository, log logr.Logger, k8sClient K8sClient, wm WatchManager) error {
	return ProcessTemplateResources(template, re, log, k8sClient, wm)
}

func processTemplateResources(
	template *kumquatv1beta1.Template,
	re repository.Repository,
	log logr.Logger,
	k8sClient K8sClient,
	wm WatchManager,
) error {
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(template)
	if err != nil {
		log.Error(err, "failed to convert template to unstructured map")
		return err
	}

	resource, err := repository.MakeResource(objMap)
	if err != nil {
		log.Error(err, "unable to make resource from object")
		return err
	}

	t, err := kumquatTemplate.NewTemplate(resource)
	if err != nil {
		log.Error(err, "unable to create template from resource")
		return err
	}

	o, err := t.Evaluate(re)
	if err != nil {
		log.Error(err, "unable to evaluate template")
		return err
	}

	desiredResourceIdentifiersSet := mapset.NewSet[ResourceIdentifier]()
	desiredResources := make(map[ResourceIdentifier]*unstructured.Unstructured)

	// Process each resource generated by the template
	for i := 0; i < o.Output.ResourceCount(); i++ {
		out, err := o.Output.ResultString(i)
		if err != nil {
			log.Error(err, "unable to get result string")
			return err
		}

		jsonData, err := yaml.YAMLToJSON([]byte(out))
		if err != nil {
			log.Error(err, "unable to convert YAML to JSON")
			return err
		}

		unstructuredObj := &unstructured.Unstructured{}
		err = unstructuredObj.UnmarshalJSON(jsonData)
		if err != nil {
			log.Error(err, "unable to unmarshal JSON")
			return err
		}

		ri := ResourceIdentifier{
			Group:     unstructuredObj.GroupVersionKind().Group,
			Kind:      unstructuredObj.GroupVersionKind().Kind,
			Namespace: unstructuredObj.GetNamespace(),
			Name:      unstructuredObj.GetName(),
		}
		desiredResources[ri] = unstructuredObj

		desiredResourceIdentifiersSet.Add(ri)
	}

	// Retrieve existing resources generated by this template
	existingResourceIdentifiers := wm.GetGeneratedResources(template.Name)
	if existingResourceIdentifiers == nil {
		existingResourceIdentifiers = mapset.NewSet[ResourceIdentifier]()
	}
	fmt.Println("Existing resources", existingResourceIdentifiers)
	// Convert the existing resources to a set for easy comparison

	resourcesToDelete := existingResourceIdentifiers.Difference(desiredResourceIdentifiersSet)

	// Delete unwanted resources
	for ri := range resourcesToDelete.Iter() {
		err := deleteResource(ri, k8sClient, log)
		if err != nil {
			log.Error(err, "unable to delete resource", "resource", ri)
			return err
		}
	}
	// Apply desired resources (create or update)
	for ri := range desiredResourceIdentifiersSet.Iter() {
		_, err := k8sClient.CreateOrUpdate(context.Background(), desiredResources[ri])
		if err != nil {
			log.Error(err, "unable to create or update resource", "resource", ri)
			return err
		}
	}

	// Update the generatedResources in WatchManager
	wm.UpdateGeneratedResources(template.Name, desiredResourceIdentifiersSet)
	return nil
}

func deleteResource(ri ResourceIdentifier, k8sClient K8sClient, log logr.Logger) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: ri.Group,
		Kind:  ri.Kind,
	})
	obj.SetNamespace(ri.Namespace)
	obj.SetName(ri.Name)

	err := k8sClient.Delete(context.Background(), obj.GroupVersionKind().Group,
		obj.GetKind(), obj.GetNamespace(), obj.GetName())
	if err != nil && !strings.Contains(err.Error(), "not found") {
		log.Error(err, "unable to delete resource", "resource", ri)
		return err
	}
	log.Info("Deleted resource", "resource", ri)
	return nil
}
