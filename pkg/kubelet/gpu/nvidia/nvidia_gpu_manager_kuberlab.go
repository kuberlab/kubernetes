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

package nvidia

import (
	"fmt"
	"io/ioutil"
	"regexp"
	"strings"
	"sync"

	"github.com/golang/glog"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/pkg/kubelet/dockershim/libdocker"
	"k8s.io/kubernetes/pkg/kubelet/gpu"
	"strconv"
)

// TODO: rework to use Nvidia's NVML, which is more complex, but also provides more fine-grained information and stats.
const (
	devDirectory2   = "/dev"
	nvidiaDeviceRE2 = `^nvidia[0-9]*$`
)


// nvidiaGPUManager manages nvidia gpu devices.
type nvidiaGPUManager2 struct {
	sync.Mutex
	allGPUs          sets.String
	allocated        *podGPUs
	dockerClient     libdocker.Interface
	activePodsLister activePodsLister
}

// NewNvidiaGPUManager returns a GPUManager that manages local Nvidia GPUs.
// TODO: Migrate to use pod level cgroups and make it generic to all runtimes.
func NewNvidiaGPUManager2(activePodsLister activePodsLister, dockerClient libdocker.Interface) (gpu.GPUManager, error) {
	if dockerClient == nil {
		return nil, fmt.Errorf("invalid docker client specified")
	}
	return &nvidiaGPUManager2{
		allGPUs:          sets.NewString(),
		dockerClient:     dockerClient,
		activePodsLister: activePodsLister,
	}, nil
}
func (ngm *nvidiaGPUManager2) NvidiaDriverType() gpu.NVIDIADockerPlugin {
	return gpu.NVIDIA_DOCKER_PLUGIN_2
}

// Initialize the GPU devices, so far only needed to discover the GPU paths.
func (ngm *nvidiaGPUManager2) Start() error {
	if ngm.dockerClient == nil {
		return fmt.Errorf("Invalid docker client specified in GPU Manager")
	}
	ngm.Lock()
	defer ngm.Unlock()

	if err := ngm.discoverGPUs(); err != nil {
		return err
	}

	// We ignore errors when identifying allocated GPUs because it is possible that the runtime interfaces may be not be logically up.
	return nil
}

// Get how many GPU cards we have.
func (ngm *nvidiaGPUManager2) Capacity() v1.ResourceList {
	gpus := resource.NewQuantity(int64(len(ngm.allGPUs)), resource.DecimalSI)
	return v1.ResourceList{
		v1.ResourceNvidiaGPU: *gpus,
	}
}

// AllocateGPUs returns `num` GPUs if available, error otherwise.
// Allocation is made thread safe using the following logic.
// A list of all GPUs allocated is maintained along with their respective Pod UIDs.
// It is expected that the list of active pods will not return any false positives.
// As part of initialization or allocation, the list of GPUs in use will be computed once.
// Whenever an allocation happens, the list of GPUs allocated is updated based on the list of currently active pods.
// GPUs allocated to terminated pods are freed up lazily as part of allocation.
// GPUs are allocated based on the internal list of allocatedGPUs.
// It is not safe to generate a list of GPUs in use by inspecting active containers because of the delay between GPU allocation and container creation.
// A GPU allocated to a container might be re-allocated to a subsequent container because the original container wasn't started quick enough.
// The current algorithm scans containers only once and then uses a list of active pods to track GPU usage.
// This is a sub-optimal solution and a better alternative would be that of using pod level cgroups instead.
// GPUs allocated to containers should be reflected in pod level device cgroups before completing allocations.
// The pod level cgroups will then serve as a checkpoint of GPUs in use.
func (ngm *nvidiaGPUManager2) AllocateGPU(pod *v1.Pod, container *v1.Container) ([]string, error) {
	gpusNeeded := container.Resources.Limits.NvidiaGPU().Value()
	if gpusNeeded == 0 {
		return []string{}, nil
	}
	ngm.Lock()
	defer ngm.Unlock()
	if ngm.allocated == nil {
		// Initialization is not complete. Try now. Failures can no longer be tolerated.
		ngm.allocated = ngm.gpusInUse()
	} else {
		// update internal list of GPUs in use prior to allocating new GPUs.
		ngm.updateAllocatedGPUs()
	}
	// Check if GPUs have already been allocated. If so return them right away.
	// This can happen if a container restarts for example.
	if devices := ngm.allocated.getGPUs(string(pod.UID), container.Name); devices != nil {
		glog.V(2).Infof("Found pre-allocated GPUs for container %q in Pod %q: %v", container.Name, pod.UID, devices.List())
		return devices.List(), nil
	}
	// Get GPU devices in use.
	devicesInUse := ngm.allocated.devices()
	glog.V(5).Infof("gpus in use: %v", devicesInUse.List())
	// Get a list of available GPUs.
	available := ngm.allGPUs.Difference(devicesInUse)
	glog.V(5).Infof("gpus available: %v", available.List())
	if int64(available.Len()) < gpusNeeded {
		return nil, fmt.Errorf("requested number of GPUs unavailable. Requested: %d, Available: %d", gpusNeeded, available.Len())
	}
	ret := available.UnsortedList()[:gpusNeeded]
	for _, device := range ret {
		// Update internal allocated GPU cache.
		ngm.allocated.insert(string(pod.UID), container.Name, device)
	}

	return ret, nil
}

// updateAllocatedGPUs updates the list of GPUs in use.
// It gets a list of active pods and then frees any GPUs that are bound to terminated pods.
// Returns error on failure.
func (ngm *nvidiaGPUManager2) updateAllocatedGPUs() {
	activePods := ngm.activePodsLister.GetActivePods()
	activePodUids := sets.NewString()
	for _, pod := range activePods {
		activePodUids.Insert(string(pod.UID))
	}
	allocatedPodUids := ngm.allocated.pods()
	podsToBeRemoved := allocatedPodUids.Difference(activePodUids)
	glog.V(5).Infof("pods to be removed: %v", podsToBeRemoved.List())
	ngm.allocated.delete(podsToBeRemoved.List())
}

// discoverGPUs identifies allGPUs NVIDIA GPU devices available on the local node by walking `/dev` directory.
// TODO: Without NVML support we only can check whether there has GPU devices, but
// could not give a health check or get more information like GPU cores, memory, or
// family name. Need to support NVML in the future. But we do not need NVML until
// we want more features, features like schedule containers according to GPU family
// name.
func (ngm *nvidiaGPUManager2) discoverGPUs() error {
	reg := regexp.MustCompile(nvidiaDeviceRE2)
	files, err := ioutil.ReadDir(devDirectory2)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if p := reg.FindStringSubmatch(f.Name()); len(p) == 2 {
			glog.V(2).Infof("Found Nvidia GPU %q %s", f.Name(), p[1])
			ngm.allGPUs.Insert(p[1])
		}
	}

	return nil
}

// gpusInUse returns a list of GPUs in use along with the respective pods that are using it.
func (ngm *nvidiaGPUManager2) gpusInUse() *podGPUs {
	pods := ngm.activePodsLister.GetActivePods()
	type containerIdentifier struct {
		id   string
		name string
	}
	type podContainers struct {
		uid        string
		containers []containerIdentifier
	}
	// List of containers to inspect.
	podContainersToInspect := []podContainers{}
	for _, pod := range pods {
		containers := sets.NewString()
		for _, container := range pod.Spec.Containers {
			// GPUs are expected to be specified only in limits.
			if !container.Resources.Limits.NvidiaGPU().IsZero() {
				containers.Insert(container.Name)
			}
		}
		// If no GPUs were requested skip this pod.
		if containers.Len() == 0 {
			continue
		}
		// TODO: If kubelet restarts right after allocating a GPU to a pod, the container might not have started yet and so container status might not be available yet.
		// Use an internal checkpoint instead or try using the CRI if its checkpoint is reliable.
		var containersToInspect []containerIdentifier
		for _, container := range pod.Status.ContainerStatuses {
			if containers.Has(container.Name) {
				containersToInspect = append(containersToInspect, containerIdentifier{strings.Replace(container.ContainerID, "docker://", "", 1), container.Name})
			}
		}
		// add the pod and its containers that need to be inspected.
		podContainersToInspect = append(podContainersToInspect, podContainers{string(pod.UID), containersToInspect})
	}
	ret := newPodGPUs()
	for _, podContainer := range podContainersToInspect {
		for _, containerIdentifier := range podContainer.containers {
			containerJSON, err := ngm.dockerClient.InspectContainer(containerIdentifier.id)
			if err != nil {
				glog.V(3).Infof("Failed to inspect container %q in pod %q while attempting to reconcile nvidia gpus in use", containerIdentifier.id, podContainer.uid)
				continue
			}
			for _, e := range containerJSON.Config.Env {
				if strings.HasPrefix(e, "NVIDIA_VISIBLE_DEVICES=") {
					e = strings.TrimPrefix(e, "NVIDIA_VISIBLE_DEVICES=")
					devices := strings.Split(e, ",")
					for _, d := range devices {
						if _, err := strconv.Atoi(d); err == nil {
							ret.insert(podContainer.uid, containerIdentifier.name, d)
						}
					}
				}
			}
		}
	}
	return ret
}
