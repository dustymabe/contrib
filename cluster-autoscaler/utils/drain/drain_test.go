/*
Copyright 2016 The Kubernetes Authors.

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

package drain

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"testing"

	api "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/testapi"
	apiv1 "k8s.io/kubernetes/pkg/api/v1"
	batchv1 "k8s.io/kubernetes/pkg/apis/batch/v1"
	extensions "k8s.io/kubernetes/pkg/apis/extensions/v1beta1"
	"k8s.io/kubernetes/pkg/client/clientset_generated/release_1_5/fake"
	"k8s.io/kubernetes/pkg/client/testing/core"
	"k8s.io/kubernetes/pkg/runtime"
)

func TestDrain(t *testing.T) {
	replicas := int32(5)

	rc := apiv1.ReplicationController{
		ObjectMeta: apiv1.ObjectMeta{
			Name:      "rc",
			Namespace: "default",
			SelfLink:  testapi.Default.SelfLink("replicationcontrollers", "rc"),
		},
		Spec: apiv1.ReplicationControllerSpec{
			Replicas: &replicas,
		},
	}

	rcPod := &apiv1.Pod{
		ObjectMeta: apiv1.ObjectMeta{
			Name:        "bar",
			Namespace:   "default",
			Annotations: map[string]string{apiv1.CreatedByAnnotation: refJSON(t, &rc)},
		},
		Spec: apiv1.PodSpec{
			NodeName: "node",
		},
	}

	ds := extensions.DaemonSet{
		ObjectMeta: apiv1.ObjectMeta{
			Name:      "ds",
			Namespace: "default",
			SelfLink:  "/apiv1s/extensions/v1beta1/namespaces/default/daemonsets/ds",
		},
	}

	dsPod := &apiv1.Pod{
		ObjectMeta: apiv1.ObjectMeta{
			Name:        "bar",
			Namespace:   "default",
			Annotations: map[string]string{apiv1.CreatedByAnnotation: refJSON(t, &ds)},
		},
		Spec: apiv1.PodSpec{
			NodeName: "node",
		},
	}

	job := batchv1.Job{
		ObjectMeta: apiv1.ObjectMeta{
			Name:      "job",
			Namespace: "default",
			SelfLink:  "/apiv1s/extensions/v1beta1/namespaces/default/jobs/job",
		},
	}

	jobPod := &apiv1.Pod{
		ObjectMeta: apiv1.ObjectMeta{
			Name:        "bar",
			Namespace:   "default",
			Annotations: map[string]string{apiv1.CreatedByAnnotation: refJSON(t, &job)},
		},
	}

	rs := extensions.ReplicaSet{
		ObjectMeta: apiv1.ObjectMeta{
			Name:      "rs",
			Namespace: "default",
			SelfLink:  testapi.Default.SelfLink("replicasets", "rs"),
		},
		Spec: extensions.ReplicaSetSpec{
			Replicas: &replicas,
		},
	}

	rsPod := &apiv1.Pod{
		ObjectMeta: apiv1.ObjectMeta{
			Name:        "bar",
			Namespace:   "default",
			Annotations: map[string]string{apiv1.CreatedByAnnotation: refJSON(t, &rs)},
		},
		Spec: apiv1.PodSpec{
			NodeName: "node",
		},
	}

	nakedPod := &apiv1.Pod{
		ObjectMeta: apiv1.ObjectMeta{
			Name:      "bar",
			Namespace: "default",
		},
		Spec: apiv1.PodSpec{
			NodeName: "node",
		},
	}

	emptydirPod := &apiv1.Pod{
		ObjectMeta: apiv1.ObjectMeta{
			Name:      "bar",
			Namespace: "default",
		},
		Spec: apiv1.PodSpec{
			NodeName: "node",
			Volumes: []apiv1.Volume{
				{
					Name:         "scratch",
					VolumeSource: apiv1.VolumeSource{EmptyDir: &apiv1.EmptyDirVolumeSource{Medium: ""}},
				},
			},
		},
	}

	tests := []struct {
		description string
		pods        []*apiv1.Pod
		rcs         []apiv1.ReplicationController
		replicaSets []extensions.ReplicaSet
		expectFatal bool
		expectPods  []*apiv1.Pod
	}{
		{
			description: "RC-managed pod",
			pods:        []*apiv1.Pod{rcPod},
			rcs:         []apiv1.ReplicationController{rc},
			expectFatal: false,
			expectPods:  []*apiv1.Pod{rcPod},
		},
		{
			description: "DS-managed pod",
			pods:        []*apiv1.Pod{dsPod},
			expectFatal: false,
			expectPods:  []*apiv1.Pod{},
		},
		{
			description: "Job-managed pod",
			pods:        []*apiv1.Pod{jobPod},
			rcs:         []apiv1.ReplicationController{rc},
			expectFatal: false,
			expectPods:  []*apiv1.Pod{jobPod},
		},
		{
			description: "RS-managed pod",
			pods:        []*apiv1.Pod{rsPod},
			replicaSets: []extensions.ReplicaSet{rs},
			expectFatal: false,
			expectPods:  []*apiv1.Pod{rsPod},
		},
		{
			description: "naked pod",
			pods:        []*apiv1.Pod{nakedPod},
			expectFatal: true,
			expectPods:  []*apiv1.Pod{},
		},
		{
			description: "pod with EmptyDir",
			pods:        []*apiv1.Pod{emptydirPod},
			expectFatal: true,
			expectPods:  []*apiv1.Pod{},
		},
	}

	for _, test := range tests {

		fakeClient := &fake.Clientset{}
		register := func(resource string, obj runtime.Object, meta apiv1.ObjectMeta) {
			fakeClient.Fake.AddReactor("get", resource, func(action core.Action) (bool, runtime.Object, error) {
				getAction := action.(core.GetAction)
				if getAction.GetName() == meta.GetName() && getAction.GetNamespace() == meta.GetNamespace() {
					return true, obj, nil
				}
				return false, nil, fmt.Errorf("Not found")
			})
		}
		if len(test.rcs) > 0 {
			register("replicationcontrollers", &test.rcs[0], test.rcs[0].ObjectMeta)
		}
		register("daemonsets", &ds, ds.ObjectMeta)
		register("jobs", &job, job.ObjectMeta)
		if len(test.replicaSets) > 0 {
			register("replicasets", &test.replicaSets[0], test.replicaSets[0].ObjectMeta)
		}
		pods, err := GetPodsForDeletionOnNodeDrain(test.pods, api.Codecs.UniversalDecoder(),
			false, true, true, true, fakeClient, 0)

		if test.expectFatal {
			if err == nil {
				t.Fatalf("%s: unexpected non-error", test.description)
			}
		}

		if !test.expectFatal {
			if err != nil {
				t.Fatalf("%s: error occurred: %v", test.description, err)
			}
		}

		if len(pods) != len(test.expectPods) {
			t.Fatalf("Wrong pod list content: %v", test.description)
		}
	}
}

func refJSON(t *testing.T, o runtime.Object) string {
	ref, err := apiv1.GetReference(o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	codec := testapi.Default.Codec()
	json := runtime.EncodeOrDie(codec, &apiv1.SerializedReference{Reference: *ref})
	return string(json)
}

func objBody(codec runtime.Codec, obj runtime.Object) io.ReadCloser {
	return ioutil.NopCloser(bytes.NewReader([]byte(runtime.EncodeOrDie(codec, obj))))
}
