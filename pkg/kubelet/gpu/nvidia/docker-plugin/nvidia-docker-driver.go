package docker_plugin

import (
	"encoding/json"
	"fmt"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"net/http"
	"strings"
)

type dockerArgs struct {
	VolumeDriver string
	Volumes      []string
	Devices      []string
}

func GetNVIDIADriverMount(pluginAddress string) (binding *kubecontainer.Mount, driver string, err error) {
	resp, respErr := http.DefaultClient.Get(pluginAddress)
	if respErr != nil {
		err = fmt.Errorf("Failed read GPU driver mount points from nvidia-docker-plugin: %v", respErr)
		return
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("Failed read GPU driver mount points from nvidia-docker-plugin response status = %s", resp.Status)
		return
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(resp.Body)
	args := dockerArgs{}
	if derr := decoder.Decode(&args); derr != nil {
		err = fmt.Errorf("Failed read nvidia-docker-plugin response: %v", derr)
		return
	}
	if len(args.Volumes) > 0 {
		if mounts := strings.Split(args.Volumes[0], ":"); len(mounts) > 1 {
			binding = &kubecontainer.Mount{
				ReadOnly:      true,
				HostPath:      mounts[0],
				ContainerPath: mounts[1],
			}
			driver = args.VolumeDriver
			return
		}
	}
	return
}
