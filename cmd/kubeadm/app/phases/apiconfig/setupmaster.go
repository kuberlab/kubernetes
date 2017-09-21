/*
Copyright 2017 The Kubernetes Authors.

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

package apiconfig

import (
	"encoding/json"
	"fmt"
	"time"

	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	kubeletapis "k8s.io/kubernetes/pkg/kubelet/apis"
	"k8s.io/kubernetes/pkg/util/node"
	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
)

const apiCallRetryInterval = 500 * time.Millisecond

// TODO: Can we think of any unit tests here? Or should this code just be covered through integration/e2e tests?

func attemptToUpdateMasterRoleLabelsAndTaints(cfg *kubeadmapi.MasterConfiguration,client *clientset.Clientset) (bool,error) {
	var n *v1.Node

	// Wait for current node registration
	wait.PollInfinite(kubeadmconstants.APICallRetryInterval, func() (bool, error) {
		var err error
		if n, err = client.Nodes().Get(node.GetHostname(cfg.HostnameOverride), metav1.GetOptions{}); err != nil {
			return false, nil
		}
		// The node may appear to have no labels at first,
		// so we wait for it to get hostname label.
		_, found := n.ObjectMeta.Labels[kubeletapis.LabelHostname]
		return found, nil
	})

	oldData, err := json.Marshal(n)
	if err != nil {
		return true, err
	}

	// The master node is tainted and labelled accordingly
	n.ObjectMeta.Labels[kubeadmconstants.LabelNodeRoleMaster] = ""
	addTaintIfNotExists(n, v1.Taint{Key: kubeadmconstants.LabelNodeRoleMaster, Value: "", Effect: "NoSchedule"})

	newData, err := json.Marshal(n)
	if err != nil {
		return true, err
	}

	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, v1.Node{})
	if err != nil {
		return true, err
	}

	if _, err := client.Nodes().Patch(n.Name, types.StrategicMergePatchType, patchBytes); err != nil {
		if apierrs.IsConflict(err) {
			fmt.Println("[apiclient] Temporarily unable to update master node metadata due to conflict (will retry)")
			time.Sleep(apiCallRetryInterval)
			attemptToUpdateMasterRoleLabelsAndTaints(cfg, client)
			return false,nil
		} else {
			return true,err
		}
	}
	return true,nil
}

func addTaintIfNotExists(n *v1.Node, t v1.Taint) {
	for _, taint := range n.Spec.Taints {
		if taint == t {
			return
		}
	}

	n.Spec.Taints = append(n.Spec.Taints, t)
}

// UpdateMasterRoleLabelsAndTaints taints the master and sets the master label
func UpdateMasterRoleLabelsAndTaints(cfg *kubeadmapi.MasterConfiguration, client *clientset.Clientset) error {
	err := wait.PollInfinite(kubeadmconstants.APICallRetryInterval,
		func() (bool, error) {
			return attemptToUpdateMasterRoleLabelsAndTaints(cfg, client)
		})
	if err != nil {
		return fmt.Errorf("failed to update master node - [%v]", err)
	}
	return nil
}
