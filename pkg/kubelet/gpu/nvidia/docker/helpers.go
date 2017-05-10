package docker

import (
	"fmt"
	"github.com/NVIDIA/nvidia-docker/src/docker"
	"github.com/NVIDIA/nvidia-docker/src/nvidia"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"strings"
)

const (
	labelCUDAVersion   = "com.nvidia.cuda.version"
	labelVolumesNeeded = "com.nvidia.volumes.needed"
)

func VolumesNeeded(image string) ([]string, error) {
	ok, err := docker.ImageExists(image)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err = docker.ImagePull(image); err != nil {
			return nil, err
		}
	}

	label, err := docker.Label(image, labelVolumesNeeded)
	if err != nil {
		return nil, err
	}
	if label == "" {
		return nil, nil
	}
	return strings.Split(label, " "), nil
}

func cudaSupported(image, version string) error {
	var vmaj, vmin int
	var lmaj, lmin int

	label, err := docker.Label(image, labelCUDAVersion)
	if err != nil {
		return err
	}
	if label == "" {
		return nil
	}
	if _, err := fmt.Sscanf(version, "%d.%d", &vmaj, &vmin); err != nil {
		return err
	}
	if _, err := fmt.Sscanf(label, "%d.%d", &lmaj, &lmin); err != nil {
		return err
	}
	if lmaj > vmaj || (lmaj == vmaj && lmin > vmin) {
		return fmt.Errorf("unsupported CUDA version: driver %s < image %s", version, label)
	}
	return nil
}

func VolumesArgs(vols []string) (*kubecontainer.Mount, string, error) {
	drv, err := nvidia.GetDriverVersion()
	if err != nil {
		return nil, "", err
	}
	for _, vol := range nvidia.Volumes {
		for _, v := range vols {
			if v == vol.Name {
				// Check if the volume exists locally otherwise fallback to using the plugin
				n := fmt.Sprintf("%s_%s", vol.Name, drv)
				if _, err := docker.VolumeInspect(n); err == nil {
					return &kubecontainer.Mount{
						ContainerPath: vol.Mountpoint,
						HostPath:      n,
						ReadOnly:      true,
					}, "", nil
				} else {
					return &kubecontainer.Mount{
						ContainerPath: vol.Mountpoint,
						HostPath:      n,
						ReadOnly:      true,
					}, nvidia.DockerPlugin, nil
				}
				break
			}
		}
	}
	return nil, "", nil
}
