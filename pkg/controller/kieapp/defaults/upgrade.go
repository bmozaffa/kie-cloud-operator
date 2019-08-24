package defaults

import (
	"context"
	"fmt"
	v1 "github.com/kiegroup/kie-cloud-operator/pkg/apis/app/v1"
	"github.com/kiegroup/kie-cloud-operator/pkg/controller/kieapp/shared"
	oappsv1 "github.com/openshift/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"

	"github.com/gobuffalo/packr/v2"
	"github.com/google/go-cmp/cmp"
	api "github.com/kiegroup/kie-cloud-operator/pkg/apis/app/v2"
	"github.com/kiegroup/kie-cloud-operator/pkg/controller/kieapp/constants"
	"github.com/kiegroup/kie-cloud-operator/version"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// UpgradingSchema ...
func UpgradingSchema(namespacedName types.NamespacedName, service api.PlatformService) (bool, reconcile.Result, error) {
	// Fetch v1 KieApp instance
	v1instance := &v1.KieApp{}
	log.Debugf("Checking upgrade with %v", namespacedName)
	err := service.Get(context.TODO(), namespacedName, v1instance)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Debug("No V1 objects found, so no schema upgrade needed")
			return false, reconcile.Result{}, nil
		} else {
			return true, reconcile.Result{}, err
		}
	}

	//Unsupported product versions should be left alone and not upgraded:
	if v1instance.Spec.CommonConfig.Version != "" && !CheckVersion(v1instance.Spec.CommonConfig.Version) {
		//Old unsupported version, leave it alone
		log.Debugf("Unsupported version %s found, will not upgrade", v1instance.Spec.CommonConfig.Version)
		return false, reconcile.Result{}, nil
	}

	//Create an updated CR version if one does not exist
	updatedCR := &api.KieApp{}
	err = service.Get(context.TODO(), namespacedName, updatedCR)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Debug("No v2 equivalent found, will upgrade CR")
			instance, err := api.ConvertKieAppV1toV2(v1instance)
			if err != nil {
				return true, reconcile.Result{}, err
			}
			log.Debugf("Will create upgraded CR as follows: %v", instance)
			err = service.Create(context.TODO(), instance)
			updatedCR = instance //TODO will this have the generated metadata?
			log.Debugf("Updated CR now looks like this: %v", updatedCR)
			if err != nil {
				return true, reconcile.Result{}, err
			}
		} else {
			return true, reconcile.Result{}, err
		}
	}

	listOps := &client.ListOptions{Namespace: namespacedName.Namespace}
	dcList := &oappsv1.DeploymentConfigList{}
	err = service.List(context.TODO(), listOps, dcList)
	if err != nil {
		return true, reconcile.Result{}, err
	}
	for _, dc := range dcList.Items {
		log.Debugf("Item is %v", dc)
		if shared.IsOwnedBy(&dc, v1instance.UID) {
			if !shared.IsOwnedBy(&dc, updatedCR.UID) {
				err = controllerutil.SetControllerReference(updatedCR, &dc, service.GetScheme())
				if err != nil {
					return true, reconcile.Result{}, err
				}
			}
		}
	}
	//list := &metav1.List{}
	//err = service.List(context.TODO(), listOps, list)
	//if err != nil {
	//	return true, reconcile.Result{}, err
	//}
	//for _, item := range list.Items {
	//	log.Debugf("Item is %v", item)
	//	metaObject := item.Object.(metav1.Object)
	//	if shared.IsOwnedBy(metaObject, v1instance.UID) {
	//		if !shared.IsOwnedBy(metaObject, updatedCR.UID) {
	//			err = controllerutil.SetControllerReference(updatedCR, metaObject, nil)
	//			if err != nil {
	//				return true, reconcile.Result{}, err
	//			}
	//		}
	//	}
	//}
	return false, reconcile.Result{}, nil
}

// CheckProductUpgrade ...
func checkProductUpgrade(cr *api.KieApp) (minor, micro bool, err error) {
	setDefaults(cr)
	if CheckVersion(cr.Spec.Version) {
		if cr.Spec.Version != constants.CurrentVersion && cr.Spec.Upgrades.Enabled {
			micro = cr.Spec.Upgrades.Enabled
			minor = cr.Spec.Upgrades.Minor
		}
	} else {
		err = fmt.Errorf("Product version %s is not allowed in operator version %s. The following versions are allowed - %s", cr.Spec.Version, version.Version, constants.SupportedVersions)
	}
	return minor, micro, err
}

// CheckVersion ...
func CheckVersion(productVersion string) bool {
	for _, version := range constants.SupportedVersions {
		if version == productVersion {
			return true
		}
	}
	return false
}

// getMinorImageVersion ...
func getMinorImageVersion(productVersion string) string {
	major, minor, _ := MajorMinorMicro(productVersion)
	return strings.Join([]string{major, minor}, "")
}

// MajorMinorMicro ...
func MajorMinorMicro(productVersion string) (major, minor, micro string) {
	version := strings.Split(productVersion, ".")
	for len(version) < 3 {
		version = append(version, "0")
	}
	return version[0], version[1], version[2]
}

// getConfigVersionDiffs ...
func getConfigVersionDiffs(fromVersion, toVersion string, service api.PlatformService) error {
	if CheckVersion(fromVersion) && CheckVersion(toVersion) {
		fromList, toList := getConfigVersionLists(fromVersion, toVersion)
		diffs := configDiffs(fromList, toList)
		cmDiffs := diffs
		// only check against existing configmaps if running via deployment in a cluster
		if _, depNameSpace, useEmbedded := UseEmbeddedFiles(service); !useEmbedded {
			cmFromList := map[string][]map[string]string{}
			for name := range fromList {
				nameSplit := strings.Split(name, "-")
				cmName := strings.Join(append([]string{nameSplit[0], fromVersion}, nameSplit[1:]...), "-")
				// *** need to retrieve cm of same name w/ current version and do compare against default upgrade diffs...
				currentCM := &corev1.ConfigMap{}
				if err := service.Get(context.TODO(), types.NamespacedName{Name: cmName, Namespace: depNameSpace}, currentCM); err != nil {
					return err
				}
				cmFromList[name] = append(cmFromList[name], currentCM.Data)
			}
			cmDiffs = configDiffs(cmFromList, toList)
		} else if service.IsMockService() { // test
			fromList[constants.ConfigMapPrefix] = []map[string]string{{"common.yaml": "changed"}}
			cmDiffs = configDiffs(fromList, toList)
		}
		// if conflicts, stop upgrade
		// COMPARE NEEDS IMPROVEMENT - more precise comparison? and should maybe show exact differences that conflict.
		if !cmp.Equal(diffs, cmDiffs) {
			return fmt.Errorf("Can't upgrade, potential configuration conflicts in your %s ConfigMap(s)", fromVersion)
		}
	}
	return nil
}

// getConfigVersionLists ...
func getConfigVersionLists(fromVersion, toVersion string) (configFromList, configToList map[string][]map[string]string) {
	fromList := map[string][]map[string]string{}
	toList := map[string][]map[string]string{}
	if CheckVersion(fromVersion) && CheckVersion(toVersion) {
		box := packr.New("config", "../../../../config")
		if box.HasDir(fromVersion) && box.HasDir(toVersion) {
			cmList := getCMListfromBox(box)
			for cmName, cmData := range cmList {
				cmSplit := strings.Split(cmName, "-")
				name := strings.Join(append(cmSplit[:1], cmSplit[2:]...), "-")
				if cmSplit[1] == fromVersion {
					fromList[name] = cmData
				}
				if cmSplit[1] == toVersion {
					toList[name] = cmData
				}
			}
		}
	}
	return fromList, toList
}

// configDiffs ...
func configDiffs(cmFromList, cmToList map[string][]map[string]string) map[string]string {
	configDiffs := map[string]string{}
	for cmName, fromMapSlice := range cmFromList {
		if toMapSlice, ok := cmToList[cmName]; ok {
			diff := cmp.Diff(fromMapSlice, toMapSlice)
			if diff != "" {
				configDiffs[cmName] = diff
			}
		}
	}
	return configDiffs
}
