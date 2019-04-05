package helper

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientv1 "sigs.k8s.io/controller-runtime/pkg/client"
	corev1 "k8s.io/api/core/v1"
	oappsv1 "github.com/openshift/api/apps/v1"
	buildv1 "github.com/openshift/api/build/v1"
)

func New(client clientv1.Client, options Options) (ReconciliationHelper, error) {
	if client == nil {
		return nil, fmt.Errorf("Cannot create a reconciliation helper without a client")
	}
	if options.ListItems == nil {
		options.ListItems = func(object runtime.Object) []runtime.Object {
			return []runtime.Object{}//TODO use reflection to get Items
		}
	}
	if options.GetName == nil {
		options.GetName = func(objectType ObjectType, object runtime.Object) string {
			return "Name"//TODO use reflection to get name
		}
	}
	if options.GetNamespace == nil {
		options.GetNamespace = func(objectType ObjectType, object runtime.Object) string {
			return "Namespace"//TODO use reflection to get namespace
		}
	}
	if options.CreateObject == nil {
		options.CreateObject = func(object runtime.Object) error {
			return client.Create(context.TODO(), object)
		}
	}
	if options.UpdateObject == nil {
		options.UpdateObject = func(object runtime.Object) error {
			return client.Update(context.TODO(), object)
		}
	}
	if options.DeleteObject == nil {
		options.DeleteObject = func(object runtime.Object) error {
			return client.Delete(context.TODO(), object)
		}
	}
	return &reconciliationHelper{client, options}, nil
}

type ReconciliationHelper interface {
	Reconcile(objectTypes []ObjectType, customResource metav1.Object, requestedObjects map[ObjectType][]runtime.Object) error
}

type reconciliationHelper struct {
	client clientv1.Client
	options Options
}

type Options struct {
	ListItems func(object runtime.Object) []runtime.Object
	CompareItems func(objectType ObjectType, object1 runtime.Object, object2 runtime.Object) bool
	GetName func(objectType ObjectType, object runtime.Object) string
	GetNamespace func(objectType ObjectType, object runtime.Object) string
	CreateObject func(object runtime.Object) error
	UpdateObject func(object runtime.Object) error
	DeleteObject func(object runtime.Object) error
}

type ObjectType interface {
	ListObject() runtime.Object
}

type knownType string

const (
	Pod knownType = "Pod"
	DeploymentConfig knownType = "DeploymentConfig"
	BuildConfig knownType = "BuildConfig"
)

func (obj knownType) ListObject() runtime.Object {
	if obj == Pod {
		return &corev1.PodList{}
	} else if obj == DeploymentConfig {
		return &oappsv1.DeploymentConfigList{}
	} else if obj == BuildConfig {
		return &buildv1.BuildConfigList{}
	} else {
		panic("Look like a bug in the code, should only be called for known type, but this is not recognized: " + obj)
	}
}


type ObjectTypeChanges interface {
	CreatedObjects() []runtime.Object
	UpdatedObjects() []runtime.Object
	DeletedObjects() []runtime.Object
}

type changes struct {
	created []runtime.Object
	updated []runtime.Object
	deleted []runtime.Object
}

func (changes *changes) CreatedObjects() []runtime.Object {
	return changes.created
}

func (changes *changes) UpdatedObjects() []runtime.Object {
	return changes.updated
}

func (changes *changes) DeletedObjects() []runtime.Object {
	return changes.deleted
}

func (helper *reconciliationHelper) Reconcile(objectTypes []ObjectType, customResource metav1.Object, requestedObjects map[ObjectType][]runtime.Object) error {
	changes, err := helper.compare(objectTypes, customResource, requestedObjects)
	if err != nil {
		return err
	}
	for objectType := range changes {
		typeChanges := changes[objectType]
		for _, object := range typeChanges.CreatedObjects() {
			err = helper.options.CreateObject(object)
			if err != nil {
				return err
			}
		}
		for _, object := range typeChanges.UpdatedObjects() {
			err = helper.options.UpdateObject(object)
			if err != nil {
				return err
			}
		}
		for _, object := range typeChanges.DeletedObjects() {
			err = helper.options.DeleteObject(object)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (helper *reconciliationHelper) compare(objectTypes []ObjectType, customResource metav1.Object, requested map[ObjectType][]runtime.Object) (map[ObjectType]ObjectTypeChanges, error) {
	listOps := &client.ListOptions{Namespace: customResource.GetNamespace()}
	allChanges := make(map[ObjectType]ObjectTypeChanges)
	for _, objectType := range objectTypes {
		list := objectType.ListObject()
		err := helper.client.List(context.TODO(), listOps, list)
		if err != nil {
			return nil, err
		}
		existingObjects := helper.options.ListItems(list)
		requestedObjects := requested[objectType]
		theseChanges := &changes{}
		found := make(map[runtime.Object]bool)
		for _, existing := range existingObjects {
			var existingFound bool
			for _, requested := range requestedObjects {
				getName := helper.options.GetName
				if getName(objectType, existing) == getName(objectType, requested) {
					existingFound = true
					found[requested] = true
					if helper.options.CompareItems(objectType, existing, requested) == false {
						theseChanges.updated = append(theseChanges.updated, requested)
					}
				}
			}
			if !existingFound {
				theseChanges.deleted = append(theseChanges.deleted, existing)
			}
		}
		for _, requested := range requestedObjects {
			if found[requested] == false {
				theseChanges.created = append(theseChanges.deleted, requested)
			}
		}
		allChanges[objectType] = theseChanges
	}
	return allChanges, nil
}
