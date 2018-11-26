package defaults

import (
	"github.com/imdario/mergo"
	"github.com/kiegroup/kie-cloud-operator/pkg/apis/app/v1"
	appsv1 "github.com/openshift/api/apps/v1"
	authv1 "github.com/openshift/api/authorization/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"reflect"
)

func merge(baseline *v1.CustomObject, overwrite *v1.CustomObject) {
	baseline.PersistentVolumeClaims = mergePersistentVolumeClaims(baseline.PersistentVolumeClaims, overwrite.PersistentVolumeClaims)
	baseline.ServiceAccounts = mergeServiceAccounts(baseline.ServiceAccounts, overwrite.ServiceAccounts)
	baseline.Secrets = mergeSecrets(baseline.Secrets, overwrite.Secrets)
	baseline.RoleBindings = mergeRoleBindings(baseline.RoleBindings, overwrite.RoleBindings)
	baseline.DeploymentConfigs = mergeDeploymentConfigs(baseline.DeploymentConfigs, overwrite.DeploymentConfigs)
	baseline.Services = mergeServices(baseline.Services, overwrite.Services)
	baseline.Routes = mergeRoutes(baseline.Routes, overwrite.Routes)
}

func mergePersistentVolumeClaims(baseline []corev1.PersistentVolumeClaim, overwrite []corev1.PersistentVolumeClaim) []corev1.PersistentVolumeClaim {
	if len(overwrite) == 0 {
		return baseline
	} else if len(baseline) == 0 {
		return overwrite
	} else {
		baselineRefs := getPersistentVolumeClaimReferenceSlice(baseline)
		overwriteRefs := getPersistentVolumeClaimReferenceSlice(overwrite)
		slice := make([]corev1.PersistentVolumeClaim, combinedSize(baselineRefs, overwriteRefs))
		mergeObjects(baselineRefs, overwriteRefs, slice)
		return slice
	}
}

func getPersistentVolumeClaimReferenceSlice(objects []corev1.PersistentVolumeClaim) []v1.OpenShiftObject {
	slice := make([]v1.OpenShiftObject, len(objects))
	for index, _ := range objects {
		slice[index] = &objects[index]
	}
	return slice
}

func mergeServiceAccounts(baseline []corev1.ServiceAccount, overwrite []corev1.ServiceAccount) []corev1.ServiceAccount {
	if len(overwrite) == 0 {
		return baseline
	} else if len(baseline) == 0 {
		return overwrite
	} else {
		baselineRefs := getServiceAccountReferenceSlice(baseline)
		overwriteRefs := getServiceAccountReferenceSlice(overwrite)
		slice := make([]corev1.ServiceAccount, combinedSize(baselineRefs, overwriteRefs))
		mergeObjects(baselineRefs, overwriteRefs, slice)
		return slice
	}
}

func getServiceAccountReferenceSlice(objects []corev1.ServiceAccount) []v1.OpenShiftObject {
	slice := make([]v1.OpenShiftObject, len(objects))
	for index, _ := range objects {
		slice[index] = &objects[index]
	}
	return slice
}

func mergeSecrets(baseline []corev1.Secret, overwrite []corev1.Secret) []corev1.Secret {
	if len(overwrite) == 0 {
		return baseline
	} else if len(baseline) == 0 {
		return overwrite
	} else {
		baselineRefs := getSecretReferenceSlice(baseline)
		overwriteRefs := getSecretReferenceSlice(overwrite)
		slice := make([]corev1.Secret, combinedSize(baselineRefs, overwriteRefs))
		mergeObjects(baselineRefs, overwriteRefs, slice)
		return slice
	}
}

func getSecretReferenceSlice(objects []corev1.Secret) []v1.OpenShiftObject {
	slice := make([]v1.OpenShiftObject, len(objects))
	for index, _ := range objects {
		slice[index] = &objects[index]
	}
	return slice
}

func mergeRoleBindings(baseline []authv1.RoleBinding, overwrite []authv1.RoleBinding) []authv1.RoleBinding {
	if len(overwrite) == 0 {
		return baseline
	} else if len(baseline) == 0 {
		return overwrite
	} else {
		baselineRefs := getRoleBindingReferenceSlice(baseline)
		overwriteRefs := getRoleBindingReferenceSlice(overwrite)
		slice := make([]authv1.RoleBinding, combinedSize(baselineRefs, overwriteRefs))
		mergeObjects(baselineRefs, overwriteRefs, slice)
		return slice
	}
}

func getRoleBindingReferenceSlice(objects []authv1.RoleBinding) []v1.OpenShiftObject {
	slice := make([]v1.OpenShiftObject, len(objects))
	for index, _ := range objects {
		slice[index] = &objects[index]
	}
	return slice
}

func mergeDeploymentConfigs(baseline []appsv1.DeploymentConfig, overwrite []appsv1.DeploymentConfig) []appsv1.DeploymentConfig {
	if len(overwrite) == 0 {
		return baseline
	} else if len(baseline) == 0 {
		return overwrite
	} else {
		baselineRefs := getDeploymentConfigReferenceSlice(baseline)
		overwriteRefs := getDeploymentConfigReferenceSlice(overwrite)
		slice := make([]appsv1.DeploymentConfig, combinedSize(baselineRefs, overwriteRefs))
		mergeObjects(baselineRefs, overwriteRefs, slice)
		return slice
	}
}

func getDeploymentConfigReferenceSlice(objects []appsv1.DeploymentConfig) []v1.OpenShiftObject {
	slice := make([]v1.OpenShiftObject, len(objects))
	for index, _ := range objects {
		slice[index] = &objects[index]
	}
	return slice
}

func mergeServices(baseline []corev1.Service, overwrite []corev1.Service) []corev1.Service {
	if len(overwrite) == 0 {
		return baseline
	} else if len(baseline) == 0 {
		return overwrite
	} else {
		baselineRefs := getServiceReferenceSlice(baseline)
		overwriteRefs := getServiceReferenceSlice(overwrite)
		slice := make([]corev1.Service, combinedSize(baselineRefs, overwriteRefs))
		mergeObjects(baselineRefs, overwriteRefs, slice)
		return slice
	}
}
func getServiceReferenceSlice(objects []corev1.Service) []v1.OpenShiftObject {
	slice := make([]v1.OpenShiftObject, len(objects))
	for index, _ := range objects {
		slice[index] = &objects[index]
	}
	return slice
}

func mergeRoutes(baseline []routev1.Route, overwrite []routev1.Route) []routev1.Route {
	if len(overwrite) == 0 {
		return baseline
	} else if len(baseline) == 0 {
		return overwrite
	} else {
		baselineRefs := getRouteReferenceSlice(baseline)
		overwriteRefs := getRouteReferenceSlice(overwrite)
		slice := make([]routev1.Route, combinedSize(baselineRefs, overwriteRefs))
		mergeObjects(baselineRefs, overwriteRefs, slice)
		return slice
	}
}

func getRouteReferenceSlice(objects []routev1.Route) []v1.OpenShiftObject {
	slice := make([]v1.OpenShiftObject, len(objects))
	for index, _ := range objects {
		slice[index] = &objects[index]
	}
	return slice
}

func combinedSize(baseline []v1.OpenShiftObject, overwrite []v1.OpenShiftObject) int {
	count := 0
	for _, object := range overwrite {
		_, found := findOpenShiftObject(object, baseline)
		if found == nil && object.GetAnnotations()["delete"] != "true" {
			//unique item with no counterpart in baseline, count it
			count++
		} else if found != nil && object.GetAnnotations()["delete"] == "true" {
			///Deletes the counterpart in baseline, deduct 1 since the counterpart is being counted below
			count--
		}
	}
	count += len(baseline)
	return count
}

func mergeObjects(baseline []v1.OpenShiftObject, overwrite []v1.OpenShiftObject, objectSlice interface{}) {
	slice := reflect.ValueOf(objectSlice)
	sliceIndex := 0
	for _, object := range baseline {
		_, found := findOpenShiftObject(object, overwrite)
		if found == nil {
			slice.Index(sliceIndex).Set(reflect.ValueOf(object).Elem())
			sliceIndex++
			logrus.Debugf("Not found, added %s to beginning of slice\n", object)
		} else if found.GetAnnotations()["delete"] != "true" {
			err := mergo.Merge(object, found, mergo.WithOverride)
			if err != nil {
				logrus.Errorf("Error while trying to merge %s\n", err)
			}
			slice.Index(sliceIndex).Set(reflect.ValueOf(object).Elem())
			sliceIndex++
			if found.GetAnnotations() == nil {
				annotations := make(map[string]string)
				found.SetAnnotations(annotations)
			}
			found.GetAnnotations()["delete"] = "true"
		}
	}
	for _, object := range overwrite {
		if object.GetAnnotations()["delete"] != "true" {
			slice.Index(sliceIndex).Set(reflect.ValueOf(object).Elem())
			sliceIndex++
		}
	}
}

func findOpenShiftObject(object v1.OpenShiftObject, slice []v1.OpenShiftObject) (int, v1.OpenShiftObject) {
	for index, candidate := range slice {
		if candidate.GetName() == object.GetName() {
			return index, candidate
		}
	}
	return -1, nil
}
