/*
Copyright 2020 the original author or authors.

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
	"fmt"

	kpackbuildv1alpha1 "github.com/projectriff/system/pkg/apis/thirdparty/kpack/build/v1alpha1"
	"github.com/projectriff/system/pkg/controllers"
	"github.com/projectriff/system/pkg/tracker"
	mononokev1alpha1 "github.com/spring-cloud-incubator/mononoke/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// +kubebuilder:rbac:groups=apps.mononoke.local,resources=springbootapplications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.mononoke.local,resources=springbootapplications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=build.pivotal.io,resources=images,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete

func SpringBootApplicationReconciler(c controllers.Config) *controllers.ParentReconciler {
	c.Log = c.Log.WithName("SpringBootApplication")

	return &controllers.ParentReconciler{
		Type: &mononokev1alpha1.SpringBootApplication{},
		SubReconcilers: []controllers.SubReconciler{
			SpringBootApplicationResolveLatestImage(c),
			SpringBootApplicationResolveImageMetadata(c),
			SpringBootApplicationApplyOpinions(c),
			SpringBootApplicationChildDeploymentReconciler(c),
		},

		Config: c,
	}
}

func SpringBootApplicationResolveLatestImage(c controllers.Config) controllers.SubReconciler {
	c.Log = c.Log.WithName("ResolveLatestImage")

	return &controllers.SyncReconciler{
		Sync: func(ctx context.Context, parent *mononokev1alpha1.SpringBootApplication) error {
			ref := parent.Spec.ImageRef

			if ref == nil {
				parent.Status.LatestImage = parent.Spec.Template.Spec.Containers[0].Image
				return nil
			}

			// TODO(scothis) use a duck type based informer
			switch {
			case ref.APIVersion == "build.pivotal.io/v1alpha1" && ref.Kind == "Image":
				var image kpackbuildv1alpha1.Image
				key := types.NamespacedName{Namespace: parent.Namespace, Name: ref.Name}
				// track image for new images
				c.Tracker.Track(
					tracker.NewKey(schema.GroupVersionKind{Group: "build.pivotal.io", Version: "v1alpha1", Kind: "Image"}, key),
					types.NamespacedName{Namespace: parent.Namespace, Name: parent.Name},
				)
				if err := c.Get(ctx, key, &image); err != nil {
					if apierrs.IsNotFound(err) {
						return nil
					}
					return err
				}
				if image.Status.LatestImage != "" {
					parent.Status.LatestImage = image.Status.LatestImage
				}
				return nil
			}

			return fmt.Errorf("unsupported image reference, must be a kpack image")
		},

		Config: c,
		Setup: func(mgr controllers.Manager, bldr *controllers.Builder) error {
			bldr.Watches(&source.Kind{Type: &kpackbuildv1alpha1.Image{}}, controllers.EnqueueTracked(&kpackbuildv1alpha1.Image{}, c.Tracker, c.Scheme))
			return nil
		},
	}
}

func SpringBootApplicationResolveImageMetadata(c controllers.Config) controllers.SubReconciler {
	c.Log = c.Log.WithName("ResolveImageMetadata")

	return &controllers.SyncReconciler{
		Sync: func(ctx context.Context, parent *mononokev1alpha1.SpringBootApplication) error {
			// TODO(scothis) resolve image with build metadata

			return nil
		},

		Config: c,
	}
}

func SpringBootApplicationApplyOpinions(c controllers.Config) controllers.SubReconciler {
	c.Log = c.Log.WithName("ApplyOpinions")

	return &controllers.SyncReconciler{
		Sync: func(ctx context.Context, parent *mononokev1alpha1.SpringBootApplication) error {
			// TODO(scothis) apply opinions about boot apps

			return nil
		},

		Config: c,
	}
}

func SpringBootApplicationChildDeploymentReconciler(c controllers.Config) controllers.SubReconciler {
	c.Log = c.Log.WithName("ChildDeployment")

	return &controllers.ChildReconciler{
		ParentType:    &mononokev1alpha1.SpringBootApplication{},
		ChildType:     &appsv1.Deployment{},
		ChildListType: &appsv1.DeploymentList{},

		DesiredChild: func(parent *mononokev1alpha1.SpringBootApplication) (*appsv1.Deployment, error) {
			labels := controllers.MergeMaps(parent.Labels, map[string]string{
				mononokev1alpha1.SpringBootApplicationLabelKey: parent.Name,
			})

			template := *parent.Spec.Template.DeepCopy()
			template.Labels = controllers.MergeMaps(template.Labels, labels)
			if parent.Status.LatestImage != "" {
				template.Spec.Containers[0].Image = parent.Status.LatestImage
			}

			child := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: make(map[string]string),
					Name:        parent.Name,
					Namespace:   parent.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							mononokev1alpha1.SpringBootApplicationLabelKey: parent.Name,
						},
					},
					Template: template,
				},
			}

			return child, nil
		},
		ReflectChildStatusOnParent: func(parent *mononokev1alpha1.SpringBootApplication, child *appsv1.Deployment, err error) {
			if err != nil {
				if apierrs.IsAlreadyExists(err) {
					name := err.(apierrs.APIStatus).Status().Details.Name
					parent.Status.MarkDeploymentNotOwned(name)
				}
				return
			}
			if child != nil {
				parent.Status.PropagateDeploymentStatus(&child.Status)
			}
		},
		HarmonizeImmutableFields: func(current, desired *appsv1.Deployment) {
			// don't fight with an autoscaler
			desired.Spec.Replicas = current.Spec.Replicas
		},
		MergeBeforeUpdate: func(current, desired *appsv1.Deployment) {
			current.Labels = desired.Labels
			current.Spec = desired.Spec
		},
		SemanticEquals: func(a1, a2 *appsv1.Deployment) bool {
			return equality.Semantic.DeepEqual(a1.Spec, a2.Spec) &&
				equality.Semantic.DeepEqual(a1.Labels, a2.Labels)
		},

		Config:     c,
		IndexField: ".metadata.deploymentController",
		Sanitize: func(child *appsv1.Deployment) interface{} {
			return child.Spec
		},
	}
}
