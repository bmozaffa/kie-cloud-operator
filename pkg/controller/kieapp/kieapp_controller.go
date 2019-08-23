package kieapp

import (
	"context"
	"fmt"
	"github.com/RHsyseng/operator-utils/pkg/resource"
	"github.com/RHsyseng/operator-utils/pkg/resource/compare"
	"github.com/RHsyseng/operator-utils/pkg/resource/write"
	"reflect"
	"regexp"
	"strings"
	"time"

	v1 "github.com/kiegroup/kie-cloud-operator/pkg/apis/app/v1"
	"github.com/kiegroup/kie-cloud-operator/pkg/controller/kieapp/constants"
	"github.com/kiegroup/kie-cloud-operator/pkg/controller/kieapp/defaults"
	"github.com/kiegroup/kie-cloud-operator/pkg/controller/kieapp/logs"
	"github.com/kiegroup/kie-cloud-operator/pkg/controller/kieapp/shared"
	"github.com/kiegroup/kie-cloud-operator/pkg/controller/kieapp/status"
	"github.com/kiegroup/kie-cloud-operator/version"
	oappsv1 "github.com/openshift/api/apps/v1"
	buildv1 "github.com/openshift/api/build/v1"
	oimagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var log = logs.GetLogger("kieapp.controller")

// Reconciler reconciles a KieApp object
type Reconciler struct {
	Service v1.PlatformService
}

// Reconcile reads that state of the cluster for a KieApp object and makes changes based on the state read
// and what is in the KieApp.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (reconciler *Reconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	if opName, depNameSpace, useEmbedded := defaults.UseEmbeddedFiles(reconciler.Service); !useEmbedded {
		myDep := &appsv1.Deployment{}
		err := reconciler.Service.Get(context.TODO(), types.NamespacedName{Namespace: depNameSpace, Name: opName}, myDep)
		if err == nil {
			// Reconcile ConfigMaps
			reconciler.CreateConfigMaps(myDep)
			if shouldDeployConsole() {
				deployConsole(reconciler, myDep)
			}
		} else {
			log.Error("Can't properly create ConfigMaps. ", err)
		}
	}

	// Fetch the KieApp instance
	instance := &v1.KieApp{}
	err := reconciler.Service.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		reconciler.setFailedStatus(instance, v1.UnknownReason, err)
		return reconcile.Result{}, err
	}
	if instance.GetAnnotations() == nil {
		instance.SetAnnotations(map[string]string{
			v1.SchemeGroupVersion.Group: version.Version,
		})
	}

	deployed, err := reconciler.getDeployedResources(instance)
	if err != nil {
		reconciler.setFailedStatus(instance, v1.UnknownReason, err)
		return reconcile.Result{}, err
	}
	env, err := defaults.GetEnvironment(instance, reconciler.Service)
	if err != nil {
		reconciler.setFailedStatus(instance, v1.ConfigurationErrorReason, err)
		return reconcile.Result{}, err
	}
	env = reconciler.setEnvironmentProperties(instance, env)
	requestedResources := reconciler.getKubernetesResources(instance, env)
	for index := range requestedResources {
		requestedResources[index].SetNamespace(instance.Namespace)
	}
	requested := compare.NewMapBuilder().DisparateTypeItems(requestedResources...).Map()
	comparator := compare.NewMapComparator()
	ignoreSecretDataValues(&comparator)
	deltas := comparator.Compare(deployed, requested)
	var hasUpdates bool
	for resourceType, delta := range deltas {
		log.Debugf("Will create %d, update %d, and delete %d instances of %v", len(delta.Added), len(delta.Updated), len(delta.Removed), resourceType)
		added, err := write.AddResources(instance, reconciler.Service.GetScheme(), reconciler.Service, delta.Added)
		if err != nil {
			return reconcile.Result{}, err
		}
		updated, err := write.UpdateResources(instance, deployed[resourceType], reconciler.Service.GetScheme(), reconciler.Service, delta.Updated)
		if err != nil {
			return reconcile.Result{}, err
		}
		removed, err := write.RemoveResources(reconciler.Service, delta.Removed)
		if err != nil {
			return reconcile.Result{}, err
		}
		hasUpdates = hasUpdates || added || updated || removed
	}
	if hasUpdates && status.SetProvisioning(instance) {
		return reconciler.UpdateObj(instance)
	}

	// Check the KieServer ConfigMaps for necessary changes
	reconciler.checkKieServerConfigMap(instance, env)

	// Fetch the cached KieApp instance
	cachedInstance := &v1.KieApp{}
	err = reconciler.Service.GetCached(context.TODO(), request.NamespacedName, cachedInstance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		reconciler.setFailedStatus(instance, v1.UnknownReason, err)
		return reconcile.Result{}, err
	}

	// Update CR if needed
	if reconciler.hasSpecChanges(instance, cachedInstance) {
		if status.SetProvisioning(instance) && instance.ResourceVersion == cachedInstance.ResourceVersion {
			return reconciler.UpdateObj(instance)
		}
		return reconcile.Result{Requeue: true}, nil
	}
	if reconciler.hasStatusChanges(instance, cachedInstance) {
		if instance.ResourceVersion == cachedInstance.ResourceVersion {
			return reconciler.UpdateObj(instance)
		}
		return reconcile.Result{Requeue: true}, nil
	}
	if status.SetDeployed(instance) {
		if instance.ResourceVersion == cachedInstance.ResourceVersion {
			return reconciler.UpdateObj(instance)
		}
		return reconcile.Result{Requeue: true}, nil
	}

	return reconcile.Result{}, nil
}

func ignoreSecretDataValues(comparator *compare.MapComparator) {
	secretType := reflect.TypeOf(corev1.Secret{})
	secretComparator := comparator.Comparator.GetComparator(secretType)
	newSecretComparator := func(deployed resource.KubernetesResource, requested resource.KubernetesResource) bool {
		secret1 := deployed.(*corev1.Secret).DeepCopy()
		secret2 := requested.(*corev1.Secret).DeepCopy()
		for key := range secret1.Data {
			secret1.Data[key] = []byte{}
		}
		for key := range secret2.Data {
			secret2.Data[key] = []byte{}
		}
		return secretComparator(secret1, secret2)
	}
	comparator.Comparator.SetComparator(secretType, newSecretComparator)
}

func (reconciler *Reconciler) hasSpecChanges(instance, cached *v1.KieApp) bool {
	if !reflect.DeepEqual(instance.Spec, cached.Spec) {
		return true
	}
	if len(instance.Spec.Objects.Servers) > 0 {
		if len(instance.Spec.Objects.Servers) != len(cached.Spec.Objects.Servers) {
			return true
		}
		for i := range instance.Spec.Objects.Servers {
			if !reflect.DeepEqual(instance.Spec.Objects.Servers[i], cached.Spec.Objects.Servers[i]) {
				return true
			}
		}
	}
	return false
}

func (reconciler *Reconciler) hasStatusChanges(instance, cached *v1.KieApp) bool {
	if !reflect.DeepEqual(instance.Status, cached.Status) {
		return true
	}
	return false
}

func (reconciler *Reconciler) setFailedStatus(instance *v1.KieApp, reason v1.ReasonType, err error) {
	status.SetFailed(instance, reason, err)
	_, updateError := reconciler.UpdateObj(instance)
	if updateError != nil {
		log.Warn("Unable to update object after receiving failed status. ", err)
	}
}

// Check ImageStream
func (reconciler *Reconciler) checkImageStreamTag(name, namespace string) bool {
	log := log.With("kind", "ImageStreamTag", "name", name, "namespace", namespace)
	result := strings.Split(name, ":")
	if len(result) == 1 {
		result = append(result, "latest")
	}
	tagName := fmt.Sprintf("%s:%s", result[0], result[1])
	_, err := reconciler.Service.ImageStreamTags(namespace).Get(tagName, metav1.GetOptions{})
	if err != nil {
		log.Debug("Object does not exist")
		return false
	}
	return true
}

// Create local ImageStreamTag
func (reconciler *Reconciler) createLocalImageTag(tagRefName string, cr *v1.KieApp) error {
	result := strings.Split(tagRefName, ":")
	if len(result) == 1 {
		result = append(result, "latest")
	}
	product := defaults.GetProduct(cr.Spec.Environment)
	tagName := fmt.Sprintf("%s:%s", result[0], result[1])
	versionInfo := []byte(cr.Spec.CommonConfig.Version)
	imageName := tagName
	regContext := fmt.Sprintf("%s-%s", product, string(versionInfo[0]))

	// default registry settings
	registry := &v1.KieAppRegistry{
		Insecure: logs.GetBoolEnv("INSECURE"),
	}
	if cr.Spec.ImageRegistry != nil {
		registry = cr.Spec.ImageRegistry
	}
	if registry.Registry == "" {
		registry.Registry = logs.GetEnv("REGISTRY", constants.ImageRegistry)
	}
	registryAddress := registry.Registry
	if strings.Contains(result[0], "datagrid") {
		registryAddress = constants.ImageRegistry
		regContext = "jboss-datagrid-7"
	} else if strings.Contains(result[0], "amq-broker-7") {
		registryAddress = constants.ImageRegistry
		regContext = "amq-broker-7"
	} else if result[0] == "postgresql" || result[0] == "mysql" {
		registryAddress = constants.ImageRegistry
		regContext = "rhscl"
		pattern := regexp.MustCompile("[0-9]+")
		imageName = fmt.Sprintf("%s-%s-rhel7:%s", result[0], strings.Join(pattern.FindAllString(result[1], -1), ""), "latest")
	}
	registryURL := fmt.Sprintf("%s/%s/%s", registryAddress, regContext, imageName)

	isnew := &oimagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tagName,
			Namespace: cr.Namespace,
		},
		Tag: &oimagev1.TagReference{
			Name: result[1],
			From: &corev1.ObjectReference{
				Kind: "DockerImage",
				Name: registryURL,
			},
			ReferencePolicy: oimagev1.TagReferencePolicy{
				Type: oimagev1.LocalTagReferencePolicy,
			},
		},
	}
	isnew.SetGroupVersionKind(oimagev1.SchemeGroupVersion.WithKind("ImageStreamTag"))
	if registry.Insecure {
		isnew.Tag.ImportPolicy = oimagev1.TagImportPolicy{
			Insecure: true,
		}
	}
	log := log.With("kind", isnew.GetObjectKind().GroupVersionKind().Kind, "name", isnew.Name, "from", isnew.Tag.From.Name, "namespace", isnew.Namespace)
	log.Info("Creating")
	_, err := reconciler.Service.ImageStreamTags(isnew.Namespace).Create(isnew)
	if err != nil && !errors.IsAlreadyExists(err) {
		log.Error("Issue creating object. ", err)
		return err
	}
	return nil
}

func (reconciler *Reconciler) setEnvironmentProperties(cr *v1.KieApp, env v1.Environment) v1.Environment {
	// console keystore generation
	if !env.Console.Omit {
		consoleCN := ""
		for _, rt := range env.Console.Routes {
			if checkTLS(rt.Spec.TLS) {
				// use host of first tls route in env template
				consoleCN = reconciler.GetRouteHost(rt, cr)
				cr.Status.ConsoleHost = fmt.Sprintf("https://%s", consoleCN)
				break
			}
		}
		if consoleCN == "" {
			consoleCN = cr.Spec.CommonConfig.ApplicationName
			cr.Status.ConsoleHost = fmt.Sprintf("http://%s", consoleCN)
		}

		defaults.ConfigureHostname(&env.Console, cr, consoleCN)
		if cr.Spec.Objects.Console.KeystoreSecret == "" {
			env.Console.Secrets = append(env.Console.Secrets, generateKeystoreSecret(
				fmt.Sprintf(constants.KeystoreSecret, strings.Join([]string{cr.Spec.CommonConfig.ApplicationName, "businesscentral"}, "-")),
				consoleCN,
				cr,
			))
		}
	}

	// server(s) keystore generation
	for i, server := range env.Servers {
		if server.Omit {
			break
		}
		serverCN := ""
		for _, rt := range server.Routes {
			if checkTLS(rt.Spec.TLS) {
				// use host of first tls route in env template
				serverCN = reconciler.GetRouteHost(rt, cr)
				break
			}
		}
		if serverCN == "" {
			serverCN = cr.Spec.CommonConfig.ApplicationName
		}
		defaults.ConfigureHostname(&server, cr, serverCN)
		serverSet, kieDeploymentName := defaults.GetServerSet(cr, i)
		if serverSet.KeystoreSecret == "" {
			server.Secrets = append(server.Secrets, generateKeystoreSecret(
				fmt.Sprintf(constants.KeystoreSecret, kieDeploymentName),
				serverCN,
				cr,
			))
		}
		env.Servers[i] = server
	}

	// smartrouter keystore generation
	if !env.SmartRouter.Omit {
		smartCN := ""
		for _, rt := range env.SmartRouter.Routes {
			if checkTLS(rt.Spec.TLS) {
				// use host of first tls route in env template
				smartCN = reconciler.GetRouteHost(rt, cr)
				break
			}
		}
		if smartCN == "" {
			smartCN = cr.Spec.CommonConfig.ApplicationName
		}

		defaults.ConfigureHostname(&env.SmartRouter, cr, smartCN)
		if cr.Spec.Objects.SmartRouter.KeystoreSecret == "" {
			env.SmartRouter.Secrets = append(env.SmartRouter.Secrets, generateKeystoreSecret(
				fmt.Sprintf(constants.KeystoreSecret, strings.Join([]string{cr.Spec.CommonConfig.ApplicationName, "smartrouter"}, "-")),
				smartCN,
				cr,
			))
		}
	}
	return defaults.ConsolidateObjects(env, cr)
}

func (reconciler *Reconciler) getKubernetesResources(cr *v1.KieApp, env v1.Environment) []resource.KubernetesResource {
	var resources []resource.KubernetesResource
	resources = append(resources, reconciler.getCustomObjectResources(env.Console, cr)...)
	resources = append(resources, reconciler.getCustomObjectResources(env.SmartRouter, cr)...)
	for _, s := range env.Servers {
		resources = append(resources, reconciler.getCustomObjectResources(s, cr)...)
	}
	for _, o := range env.Others {
		resources = append(resources, reconciler.getCustomObjectResources(o, cr)...)
	}
	return resources
}

func generateKeystoreSecret(secretName, keystoreCN string, cr *v1.KieApp) corev1.Secret {
	return corev1.Secret{
		Type: corev1.SecretTypeOpaque,
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
			Labels: map[string]string{
				"app":         cr.Spec.CommonConfig.ApplicationName,
				"application": cr.Spec.CommonConfig.ApplicationName,
			},
		},
		Data: map[string][]byte{
			"keystore.jks": shared.GenerateKeystore(keystoreCN, "jboss", []byte(cr.Spec.CommonConfig.KeyStorePassword)),
		},
	}
}

// getCustomObjectResources returns all kubernetes resources that need to be created for the given CustomObject
func (reconciler *Reconciler) getCustomObjectResources(object v1.CustomObject, cr *v1.KieApp) []resource.KubernetesResource {
	var allObjects []resource.KubernetesResource
	if object.Omit {
		return allObjects
	}
	for index := range object.PersistentVolumeClaims {
		object.PersistentVolumeClaims[index].SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PersistentVolumeClaim"))
		allObjects = append(allObjects, &object.PersistentVolumeClaims[index])
	}
	for index := range object.ServiceAccounts {
		object.ServiceAccounts[index].SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ServiceAccount"))
		allObjects = append(allObjects, &object.ServiceAccounts[index])
	}
	for index := range object.Secrets {
		object.Secrets[index].SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
		allObjects = append(allObjects, &object.Secrets[index])
	}
	for index := range object.Roles {
		object.Roles[index].SetGroupVersionKind(rbacv1.SchemeGroupVersion.WithKind("Role"))
		allObjects = append(allObjects, &object.Roles[index])
	}
	for index := range object.RoleBindings {
		object.RoleBindings[index].SetGroupVersionKind(rbacv1.SchemeGroupVersion.WithKind("RoleBinding"))
		allObjects = append(allObjects, &object.RoleBindings[index])
	}
	for index := range object.DeploymentConfigs {
		object.DeploymentConfigs[index].SetGroupVersionKind(oappsv1.SchemeGroupVersion.WithKind("DeploymentConfig"))
		if len(object.BuildConfigs) == 0 {
			for ti, trigger := range object.DeploymentConfigs[index].Spec.Triggers {
				if trigger.Type == oappsv1.DeploymentTriggerOnImageChange {
					object.DeploymentConfigs[index].Spec.Triggers[ti].ImageChangeParams.From.Namespace, _ = reconciler.ensureImageStream(
						trigger.ImageChangeParams.From.Name,
						trigger.ImageChangeParams.From.Namespace,
						cr,
					)
				}
			}
		}
		allObjects = append(allObjects, &object.DeploymentConfigs[index])
	}
	for index := range object.Services {
		object.Services[index].SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
		allObjects = append(allObjects, &object.Services[index])
	}
	for index := range object.StatefulSets {
		object.StatefulSets[index].SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("StatefulSet"))
		allObjects = append(allObjects, &object.StatefulSets[index])
	}
	for index := range object.Routes {
		object.Routes[index].SetGroupVersionKind(routev1.SchemeGroupVersion.WithKind("Route"))
		allObjects = append(allObjects, &object.Routes[index])
	}
	for index := range object.ImageStreams {
		object.ImageStreams[index].SetGroupVersionKind(oimagev1.SchemeGroupVersion.WithKind("ImageStream"))
		allObjects = append(allObjects, &object.ImageStreams[index])
	}
	for index := range object.BuildConfigs {
		object.BuildConfigs[index].SetGroupVersionKind(buildv1.SchemeGroupVersion.WithKind("BuildConfig"))
		if object.BuildConfigs[index].Spec.Strategy.Type == buildv1.SourceBuildStrategyType {
			object.BuildConfigs[index].Spec.Strategy.SourceStrategy.From.Namespace, _ = reconciler.ensureImageStream(
				object.BuildConfigs[index].Spec.Strategy.SourceStrategy.From.Name,
				object.BuildConfigs[index].Spec.Strategy.SourceStrategy.From.Namespace, cr,
			)
		}
		allObjects = append(allObjects, &object.BuildConfigs[index])
	}
	return allObjects
}

func (reconciler *Reconciler) ensureImageStream(name string, namespace string, cr *v1.KieApp) (string, error) {
	if cr.Spec.ImageRegistry != nil {
		if reconciler.checkImageStreamTag(name, cr.Namespace) {
			return cr.Namespace, nil
		}
		log.Warnf("ImageStreamTag %s/%s doesn't exist.", namespace, name)
		err := reconciler.createLocalImageTag(name, cr)
		if err != nil {
			log.Error(err)
			return namespace, err
		}
		return cr.Namespace, nil
	}

	if reconciler.checkImageStreamTag(name, namespace) {
		return namespace, nil
	} else if reconciler.checkImageStreamTag(name, cr.Namespace) {
		return cr.Namespace, nil
	} else {
		log.Warnf("ImageStreamTag %s/%s doesn't exist.", namespace, name)
		err := reconciler.createLocalImageTag(name, cr)
		if err != nil {
			log.Error(err)
			return namespace, err
		}
	}
	return cr.Namespace, nil
}

// createObj creates an object based on the error passed in from a `client.Get`
func (reconciler *Reconciler) createObj(object resource.KubernetesResource, err error) (reconcile.Result, error) {
	log := log.With("kind", object.GetObjectKind().GroupVersionKind().Kind, "name", object.GetName(), "namespace", object.GetNamespace())

	if err != nil && errors.IsNotFound(err) {
		// Define a new Object
		log.Info("Creating")
		err = reconciler.Service.Create(context.TODO(), object)
		if err != nil {
			log.Warn("Failed to create object. ", err)
			return reconcile.Result{}, err
		}
		// Object created successfully - return and requeue
		return reconcile.Result{RequeueAfter: time.Duration(200) * time.Millisecond}, nil
	} else if err != nil {
		log.Error("Failed to get object. ", err)
		return reconcile.Result{}, err
	}
	log.Debug("Skip reconcile - object already exists")
	return reconcile.Result{}, nil
}

// UpdateObj reconciles the given object
func (reconciler *Reconciler) UpdateObj(obj v1.OpenShiftObject) (reconcile.Result, error) {
	log := log.With("kind", obj.GetObjectKind().GroupVersionKind().Kind, "name", obj.GetName(), "namespace", obj.GetNamespace())
	log.Info("Updating")
	err := reconciler.Service.Update(context.TODO(), obj)
	if err != nil {
		log.Warn("Failed to update object. ", err)
		return reconcile.Result{}, err
	}
	// Object updated - return and requeue
	return reconcile.Result{Requeue: true}, nil
}

func checkTLS(tls *routev1.TLSConfig) bool {
	if tls != nil {
		return true
	}
	return false
}

// GetRouteHost returns the Hostname of the route provided
func (reconciler *Reconciler) GetRouteHost(route routev1.Route, cr *v1.KieApp) string {
	route.SetGroupVersionKind(routev1.SchemeGroupVersion.WithKind("Route"))
	log := log.With("kind", route.GetObjectKind().GroupVersionKind().Kind, "name", route.Name, "namespace", route.Namespace)
	err := controllerutil.SetControllerReference(cr, &route, reconciler.Service.GetScheme())
	if err != nil {
		log.Error("Error setting controller reference. ", err)
	}
	route.SetNamespace(cr.Namespace)
	found := &routev1.Route{}
	err = reconciler.Service.Get(context.TODO(), types.NamespacedName{Name: route.Name, Namespace: route.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		_, err = reconciler.createObj(
			&route,
			err,
		)
		if err != nil {
			log.Error("Error creating Route. ", err)
		}
	}

	found = &routev1.Route{}
	for i := 1; i < 60; i++ {
		time.Sleep(time.Duration(100) * time.Millisecond)
		err = reconciler.Service.Get(context.TODO(), types.NamespacedName{Name: route.Name, Namespace: route.Namespace}, found)
		if err == nil {
			break
		}
	}
	if err != nil {
		log.Error("Error getting Route. ", err)
	}

	return found.Spec.Host
}

// CreateConfigMaps generates & creates necessary ConfigMaps from embedded files
func (reconciler *Reconciler) CreateConfigMaps(myDep *appsv1.Deployment) {
	configMaps := defaults.ConfigMapsFromFile(myDep, myDep.Namespace, reconciler.Service.GetScheme())
	for _, configMap := range configMaps {
		var testDir bool
		result := strings.Split(configMap.Name, "-")
		if len(result) > 1 {
			if result[1] == "testdata" {
				testDir = true
			}
		}
		// don't create configmaps for test directories
		if !testDir {
			// if configmap already exists, compare to new
			if existingCM, exists := reconciler.createConfigMap(&configMap); exists {
				// if new configmap and existing have different data
				if !reflect.DeepEqual(configMap.Data, existingCM.Data) || !reflect.DeepEqual(configMap.BinaryData, existingCM.BinaryData) {
					log.Infof("Differences detected in %s ConfigMap.", configMap.Name)
					existingCM.Name = strings.Join([]string{configMap.Name, "bak"}, "-")
					for annotation, ver := range configMap.Annotations {
						if annotation == v1.SchemeGroupVersion.Group {
							existingCM.Name = strings.Join([]string{configMap.Name, ver, "bak"}, "-")
						}
					}
					existingCM.ResourceVersion = ""
					existingCM.OwnerReferences = nil
					// create a backup configmap of existing
					// if backup configmap already exists, overwrite w/ new backup
					if existingBackupCM, exists := reconciler.createConfigMap(existingCM); exists {
						// if backup configmap and existing backup have different data
						if !reflect.DeepEqual(existingCM.Data, existingBackupCM.Data) || !reflect.DeepEqual(existingCM.BinaryData, existingBackupCM.BinaryData) {
							existingBackupCM.Data = existingCM.Data
						_:
							reconciler.UpdateObj(existingBackupCM)
						}
					}
				}
			}
		}
	}
}

// createConfigMap creates an individual ConfigMap, will return the existing ConfigMap object should one exist
func (reconciler *Reconciler) createConfigMap(obj v1.OpenShiftObject) (*corev1.ConfigMap, bool) {
	emptyObj := &corev1.ConfigMap{}
	err := reconciler.Service.Get(context.TODO(), types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, emptyObj)
	if errors.IsNotFound(err) {
		// attempt creation of configmap if doesn't exist
	_:
		reconciler.createObj(obj, err)
		return &corev1.ConfigMap{}, false
	} else if err != nil {
		log.Error(err)
		return &corev1.ConfigMap{}, false
	}
	return emptyObj, true
}

// checkKieServerConfigMap checks ConfigMaps owned by Kie Servers
func (reconciler *Reconciler) checkKieServerConfigMap(instance *v1.KieApp, env v1.Environment) {
	listOps := &client.ListOptions{Namespace: instance.Namespace}
	cmList := &corev1.ConfigMapList{}
	if err := reconciler.Service.List(context.TODO(), listOps, cmList); err != nil {
		log.Warn("Failed to list ConfigMaps. ", err)
	} else {
		serverDcList := make(map[string]int32)
		for _, server := range env.Servers {
			for _, sDc := range server.DeploymentConfigs {
				serverDcList[sDc.Name] = sDc.Spec.Replicas
			}
		}
		// sort through ConfigMap list, focus on ones owned by kie servers whose replicas setting is zero
		for _, cm := range cmList.Items {
			for _, ownerRef := range cm.OwnerReferences {
				if serverDcList[ownerRef.Name] == 0 && ownerRef.Kind == "DeploymentConfig" && cm.Labels[constants.KieServerCMLabel] != "" && cm.Labels[constants.KieServerCMLabel] != "DETACHED" {
					dcObj := &oappsv1.DeploymentConfig{}
					if err := reconciler.Service.Get(context.TODO(), types.NamespacedName{Name: ownerRef.Name, Namespace: cm.Namespace}, dcObj); err != nil {
						log.Error(err)
					}
					// if server DC replicas equal zero, execute DELETE against console
					if dcObj.Status.AvailableReplicas == 0 {
						cmObj := &corev1.ConfigMap{}
						if err := reconciler.Service.Get(context.TODO(), types.NamespacedName{Name: ownerRef.Name, Namespace: cm.Namespace}, cmObj); err != nil {
							log.Error(err)
						}
						cmObj.Labels[constants.KieServerCMLabel] = "DETACHED"
						log.Infof("%s replicas set to zero so relabeling associated ConfigMap as DETACHED", cm.Name)
						if _, err = reconciler.UpdateObj(cmObj); err != nil {
							log.Error(err)
						}
					}
				}
			}
		}
	}
}

func (reconciler *Reconciler) getDeployedResources(instance *v1.KieApp) (map[reflect.Type][]resource.KubernetesResource, error) {
	log := log.With("kind", instance.Kind, "name", instance.Name, "namespace", instance.Namespace)
	resourceMap := make(map[reflect.Type][]resource.KubernetesResource)

	listOps := &client.ListOptions{Namespace: instance.Namespace}

	dcList := &oappsv1.DeploymentConfigList{}
	err := reconciler.Service.List(context.TODO(), listOps, dcList)
	if err != nil {
		log.Warn("Failed to list DCs. ", err)
		return nil, err
	}
	var dcs []resource.KubernetesResource
	for index := range dcList.Items {
		dc := dcList.Items[index]
		for _, ownerRef := range dc.GetOwnerReferences() {
			if ownerRef.UID == instance.UID {
				dcs = append(dcs, &dc)
				break
			}
		}
	}
	resourceMap[reflect.TypeOf(oappsv1.DeploymentConfig{})] = dcs

	pvcList := &corev1.PersistentVolumeClaimList{}
	err = reconciler.Service.List(context.TODO(), listOps, pvcList)
	if err != nil {
		log.Warn("Failed to list PersistentVolumeClaims. ", err)
		return nil, err
	}
	var pvcs []resource.KubernetesResource
	for index := range pvcList.Items {
		pvc := pvcList.Items[index]
		for _, ownerRef := range pvc.GetOwnerReferences() {
			if ownerRef.UID == instance.UID {
				pvcs = append(pvcs, &pvc)
				break
			}
		}
	}
	resourceMap[reflect.TypeOf(corev1.PersistentVolumeClaim{})] = pvcs

	saList := &corev1.ServiceAccountList{}
	err = reconciler.Service.List(context.TODO(), listOps, saList)
	if err != nil {
		log.Warn("Failed to list ServiceAccounts. ", err)
		return nil, err
	}
	var sas []resource.KubernetesResource
	for index := range saList.Items {
		sa := saList.Items[index]
		for _, ownerRef := range sa.GetOwnerReferences() {
			if ownerRef.UID == instance.UID {
				sas = append(sas, &sa)
				break
			}
		}
	}
	resourceMap[reflect.TypeOf(corev1.ServiceAccount{})] = sas

	//secretList := &corev1.SecretList{}
	var secrets []resource.KubernetesResource
	//err = reconciler.Service.List(context.TODO(), listOps, secretList) //TODO: can't list secrets due bug:
	// https://github.com/kubernetes-sigs/controller-runtime/issues/362
	// multiple group-version-kinds associated with type *v1.SecretList, refusing to guess at one
	// Will work around by loading known secrets instead

	for _, res := range dcs {
		dc := res.(*oappsv1.DeploymentConfig)
		for _, volume := range dc.Spec.Template.Spec.Volumes {
			if volume.Secret != nil {
				name := volume.Secret.SecretName
				secret := &corev1.Secret{}
				err := reconciler.Service.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: instance.GetNamespace()}, secret)
				if err != nil && !errors.IsNotFound(err) {
					log.Warn("Failed to load Secret", err)
					return nil, err
				}
				secrets = append(secrets, secret)
			}
		}
	}
	//if err != nil {
	//	log.Warn("Failed to list Secrets. ", err)
	//	return nil, err
	//}
	//for index := range secretList.Items {
	//	secret := secretList.Items[index]
	//	for _, ownerRef := range secret.GetOwnerReferences() {
	//		if ownerRef.UID == instance.UID {
	//			secrets = append(secrets, &secret)
	//			break
	//		}
	//	}
	//}
	resourceMap[reflect.TypeOf(corev1.Secret{})] = secrets

	roleList := &rbacv1.RoleList{}
	err = reconciler.Service.List(context.TODO(), listOps, roleList)
	if err != nil {
		log.Warn("Failed to list roles. ", err)
		return nil, err
	}
	var roles []resource.KubernetesResource
	for index := range roleList.Items {
		role := roleList.Items[index]
		for _, ownerRef := range role.GetOwnerReferences() {
			if ownerRef.UID == instance.UID {
				roles = append(roles, &role)
				break
			}
		}
	}
	resourceMap[reflect.TypeOf(rbacv1.Role{})] = roles

	roleBindingList := &rbacv1.RoleBindingList{}
	err = reconciler.Service.List(context.TODO(), listOps, roleBindingList)
	if err != nil {
		log.Warn("Failed to list roleBindings. ", err)
		return nil, err
	}
	var roleBindings []resource.KubernetesResource
	for index := range roleBindingList.Items {
		roleBinding := roleBindingList.Items[index]
		for _, ownerRef := range roleBinding.GetOwnerReferences() {
			if ownerRef.UID == instance.UID {
				roleBindings = append(roleBindings, &roleBinding)
				break
			}
		}
	}
	resourceMap[reflect.TypeOf(rbacv1.RoleBinding{})] = roleBindings

	serviceList := &corev1.ServiceList{}
	err = reconciler.Service.List(context.TODO(), listOps, serviceList)
	if err != nil {
		log.Warn("Failed to list services. ", err)
		return nil, err
	}
	var services []resource.KubernetesResource
	for index := range serviceList.Items {
		service := serviceList.Items[index]
		for _, ownerRef := range service.GetOwnerReferences() {
			if ownerRef.UID == instance.UID {
				services = append(services, &service)
				break
			}
		}
	}
	resourceMap[reflect.TypeOf(corev1.Service{})] = services

	statefulSetList := &appsv1.StatefulSetList{}
	err = reconciler.Service.List(context.TODO(), listOps, statefulSetList)
	if err != nil {
		log.Warn("Failed to list statefulSets. ", err)
		return nil, err
	}
	var statefulSets []resource.KubernetesResource
	for index := range statefulSetList.Items {
		statefulSet := statefulSetList.Items[index]
		for _, ownerRef := range statefulSet.GetOwnerReferences() {
			if ownerRef.UID == instance.UID {
				statefulSets = append(statefulSets, &statefulSet)
				break
			}
		}
	}
	resourceMap[reflect.TypeOf(appsv1.StatefulSet{})] = statefulSets

	RouteList := &routev1.RouteList{}
	err = reconciler.Service.List(context.TODO(), listOps, RouteList)
	if err != nil {
		log.Warn("Failed to list routes. ", err)
		return nil, err
	}
	var routes []resource.KubernetesResource
	for index := range RouteList.Items {
		route := RouteList.Items[index]
		for _, ownerRef := range route.GetOwnerReferences() {
			if ownerRef.UID == instance.UID {
				routes = append(routes, &route)
				break
			}
		}
	}
	resourceMap[reflect.TypeOf(routev1.Route{})] = routes

	imageStreamList := &oimagev1.ImageStreamList{}
	err = reconciler.Service.List(context.TODO(), listOps, imageStreamList)
	if err != nil {
		log.Warn("Failed to list imageStreams. ", err)
		return nil, err
	}
	var imageStreams []resource.KubernetesResource
	for index := range imageStreamList.Items {
		imageStream := imageStreamList.Items[index]
		for _, ownerRef := range imageStream.GetOwnerReferences() {
			if ownerRef.UID == instance.UID {
				imageStreams = append(imageStreams, &imageStream)
				break
			}
		}
	}
	resourceMap[reflect.TypeOf(oimagev1.ImageStream{})] = imageStreams

	buildConfigList := &buildv1.BuildConfigList{}
	err = reconciler.Service.List(context.TODO(), listOps, buildConfigList)
	if err != nil {
		log.Warn("Failed to list buildConfigs. ", err)
		return nil, err
	}
	var buildConfigs []resource.KubernetesResource
	for index := range buildConfigList.Items {
		buildConfig := buildConfigList.Items[index]
		for _, ownerRef := range buildConfig.GetOwnerReferences() {
			if ownerRef.UID == instance.UID {
				buildConfigs = append(buildConfigs, &buildConfig)
				break
			}
		}
	}
	resourceMap[reflect.TypeOf(buildv1.BuildConfig{})] = buildConfigs

	return resourceMap, nil
}
