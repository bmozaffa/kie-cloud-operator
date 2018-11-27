package defaults

import (
	"github.com/sirupsen/logrus"
	"testing"

	"github.com/kiegroup/kie-cloud-operator/pkg/apis/app/v1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	oappsv1 "github.com/openshift/api/apps/v1"
)

func TestMergeServices(t *testing.T) {
	baseline, err := getConsole("trial", "test")
	assert.Nil(t, err)
	overwrite := baseline.DeepCopy()

	service1 := baseline.Services[0]
	service1.Labels["source"] = "baseline"
	service1.Labels["baseline"] = "true"
	service2 := service1.DeepCopy()
	service2.Name = service1.Name + "-2"
	service4 := service1.DeepCopy()
	service4.Name = service1.Name + "-4"
	baseline.Services = append(baseline.Services, *service2)
	baseline.Services = append(baseline.Services, *service4)

	service1b := overwrite.Services[0]
	service1b.Labels["source"] = "overwrite"
	service1b.Labels["overwrite"] = "true"
	service3 := service1b.DeepCopy()
	service3.Name = service1b.Name + "-3"
	service5 := service1b.DeepCopy()
	service5.Name = service1b.Name + "-4"
	annotations := service5.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
		service5.Annotations = annotations
	}
	service5.Annotations["delete"] = "true"
	overwrite.Services = append(overwrite.Services, *service3)
	overwrite.Services = append(overwrite.Services, *service5)

	merge(&baseline, overwrite)
	assert.Equal(t, 3, len(baseline.Services), "Expected 3 services")
	finalService1 := baseline.Services[0]
	finalService2 := baseline.Services[1]
	finalService3 := baseline.Services[2]
	assert.Equal(t, "true", finalService1.Labels["baseline"], "Expected the baseline label to be set")
	assert.Equal(t, "true", finalService1.Labels["overwrite"], "Expected the overwrite label to also be set as part of the merge")
	assert.Equal(t, "overwrite", finalService1.Labels["source"], "Expected the source label to have been overwritten by merge")
	assert.Equal(t, "true", finalService2.Labels["baseline"], "Expected the baseline label to be set")
	assert.Equal(t, "baseline", finalService2.Labels["source"], "Expected the source label to be baseline")
	assert.Equal(t, "true", finalService3.Labels["overwrite"], "Expected the baseline label to be set")
	assert.Equal(t, "true", finalService3.Labels["overwrite"], "Expected the overwrite label to be set")
	assert.Equal(t, "overwrite", finalService3.Labels["source"], "Expected the source label to be overwrite")
	assert.Equal(t, "test-rhpamcentr-2", finalService2.Name, "Second service name should end with -2")
	assert.Equal(t, "test-rhpamcentr-2", finalService2.Name, "Second service name should end with -3")
}

func TestMergeRoutes(t *testing.T) {
	baseline, err := getConsole("trial", "test")
	assert.Nil(t, err)
	overwrite := baseline.DeepCopy()

	route1 := baseline.Routes[0]
	route1.Labels["source"] = "baseline"
	route1.Labels["baseline"] = "true"
	route2 := route1.DeepCopy()
	route2.Name = route1.Name + "-2"
	route4 := route1.DeepCopy()
	route4.Name = route1.Name + "-4"
	baseline.Routes = append(baseline.Routes, *route2)
	baseline.Routes = append(baseline.Routes, *route4)

	route1b := overwrite.Routes[0]
	route1b.Labels["source"] = "overwrite"
	route1b.Labels["overwrite"] = "true"
	route3 := route1b.DeepCopy()
	route3.Name = route1b.Name + "-3"
	route5 := route1b.DeepCopy()
	route5.Name = route1b.Name + "-4"
	annotations := route5.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
		route5.Annotations = annotations
	}
	route5.Annotations["delete"] = "true"
	overwrite.Routes = append(overwrite.Routes, *route3)
	overwrite.Routes = append(overwrite.Routes, *route5)

	merge(&baseline, overwrite)
	assert.Equal(t, 3, len(baseline.Routes), "Expected 3 routes")
	finalRoute1 := baseline.Routes[0]
	finalRoute2 := baseline.Routes[1]
	finalRoute3 := baseline.Routes[2]
	assert.Equal(t, "true", finalRoute1.Labels["baseline"], "Expected the baseline label to be set")
	assert.Equal(t, "true", finalRoute1.Labels["overwrite"], "Expected the overwrite label to also be set as part of the merge")
	assert.Equal(t, "overwrite", finalRoute1.Labels["source"], "Expected the source label to have been overwritten by merge")
	assert.Equal(t, "true", finalRoute2.Labels["baseline"], "Expected the baseline label to be set")
	assert.Equal(t, "baseline", finalRoute2.Labels["source"], "Expected the source label to be baseline")
	assert.Equal(t, "true", finalRoute3.Labels["overwrite"], "Expected the baseline label to be set")
	assert.Equal(t, "true", finalRoute3.Labels["overwrite"], "Expected the overwrite label to be set")
	assert.Equal(t, "overwrite", finalRoute3.Labels["source"], "Expected the source label to be overwrite")
	assert.Equal(t, "test-rhpamcentr-2", finalRoute2.Name, "Second route name should end with -2")
	assert.Equal(t, "test-rhpamcentr-2", finalRoute2.Name, "Second route name should end with -3")
}

func getConsole(environment string, name string) (v1.CustomObject, error) {
	cr := &v1.KieApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-ns",
		},
		Spec: v1.KieAppSpec{
			Environment: environment,
		},
	}

	env, _, err := GetEnvironment(cr)
	if err != nil {
		return v1.CustomObject{}, err
	}
	return env.Console, nil
}

func TestReferenceProblem(t *testing.T) {
	cr := &v1.KieApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-ns",
		},
		Spec: v1.KieAppSpec{
			Environment: "production",
		},
	}

	env, _, _ := GetEnvironment(cr)
	assert.NotNil(t, env, "Should not be nil")
	allObjects := []v1.OpenShiftObject{}
	for _, obj := range env.Servers[0].DeploymentConfigs {
		obj.SetGroupVersionKind(oappsv1.SchemeGroupVersion.WithKind("DeploymentConfig"))
		allObjects = append(allObjects, &obj)
		logrus.Infof("Added object called %s of type %s", obj.GetName(), obj.GetObjectKind().GroupVersionKind().Kind)
	}
	for _, obj := range allObjects {
		logrus.Infof("Slice contains object called %s of type %s", obj.GetName(), obj.GetObjectKind().GroupVersionKind().Kind)
	}
}
