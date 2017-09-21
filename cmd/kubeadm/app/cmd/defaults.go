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

package cmd

import (
	"fmt"
	"io"
	"net"
	"os"

	netutil "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/sets"
	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	kubeadmutil "k8s.io/kubernetes/cmd/kubeadm/app/util"
	tokenutil "k8s.io/kubernetes/cmd/kubeadm/app/util/token"
	authzmodes "k8s.io/kubernetes/pkg/kubeapiserver/authorizer/modes"
	"k8s.io/kubernetes/pkg/util/version"
	"k8s.io/kubernetes/pkg/cloudprovider"
	_ "k8s.io/kubernetes/pkg/cloudprovider/providers"
	"k8s.io/kubernetes/cmd/kubeadm/app/master"
	"k8s.io/kubernetes/pkg/util/node"
)

func setInitDynamicDefaults(cfg *kubeadmapi.MasterConfiguration) error {

	// Choose the right address for the API Server to advertise. If the advertise address is localhost or 0.0.0.0, the default interface's IP address is used
	// This is the same logic as the API Server uses
	ip, err := netutil.ChooseBindAddress(net.ParseIP(cfg.API.AdvertiseAddress))
	if err != nil {
		return err
	}
	cfg.API.AdvertiseAddress = ip.String()

	if cfg.PublicAddress == "" {
		cfg.PublicAddress = cfg.API.AdvertiseAddress
	}
	if cfg.HostnameOverride == "" && cfg.CloudProvider != "" && cloudprovider.IsCloudProvider(cfg.CloudProvider) {
		// If need to pass cloud config.
		var config io.Reader = nil
		if _, err = os.Stat(master.DefaultCloudConfigPath); err != nil {
			return err
		}
		config, err = os.Open(master.DefaultCloudConfigPath)
		if err != nil {
			return err
		}
		cloudSupport, err := cloudprovider.GetCloudProvider(cfg.CloudProvider, config)
		if err != nil {
			fmt.Printf("[init] WARNING: Failed to get support for cloudprovider '%s'", cfg.CloudProvider)
		} else {
			if instances, ok := cloudSupport.Instances(); ok {
				if name, err := instances.CurrentNodeName(node.GetHostname("")); err != nil {
					fmt.Printf("[init] WARNING: Failed to get node name for cloud provider '%s'",
						cfg.CloudProvider)
				} else {
					cfg.HostnameOverride = string(name)
					fmt.Printf("[init] Using Kubernetes nodename %s for cloud provider: %s\n",
						cfg.HostnameOverride, cfg.CloudProvider)
				}
			}
		}
	}

	// Validate version argument
	ver, err := kubeadmutil.KubernetesReleaseVersion(cfg.KubernetesVersion)
	if err != nil {
		return err
	}
	cfg.KubernetesVersion = ver

	// Parse the given kubernetes version and make sure it's higher than the lowest supported
	k8sVersion, err := version.ParseSemantic(cfg.KubernetesVersion)
	if err != nil {
		return fmt.Errorf("couldn't parse kubernetes version %q: %v", cfg.KubernetesVersion, err)
	}
	if k8sVersion.LessThan(kubeadmconstants.MinimumControlPlaneVersion) {
		return fmt.Errorf("this version of kubeadm only supports deploying clusters with the control plane version >= %s. Current version: %s", kubeadmconstants.MinimumControlPlaneVersion.String(), cfg.KubernetesVersion)
	}

	// Defaulting is made here because it's dependent on the version currently, which is determined above
	// TODO(luxas): Cleanup this once we have dropped v1.6 support and move this code into the API group defaulting
	cfg.AuthorizationModes = defaultAuthorizationModes(cfg.AuthorizationModes, k8sVersion)

	fmt.Printf("[init] Using Kubernetes version: %s\n", cfg.KubernetesVersion)
	fmt.Printf("[init] Using Authorization modes: %v\n", cfg.AuthorizationModes)

	// Warn about the limitations with the current cloudprovider solution.
	if cfg.CloudProvider != "" {
		fmt.Println("[init] WARNING: For cloudprovider integrations to work --cloud-provider must be set for all kubelets in the cluster.")
		fmt.Println("\t(/etc/systemd/system/kubelet.service.d/10-kubeadm.conf should be edited for this purpose)")
	}

	if cfg.Token == "" {
		var err error
		cfg.Token, err = tokenutil.GenerateToken()
		if err != nil {
			return fmt.Errorf("couldn't generate random token: %v", err)
		}
	}
	if cfg.Etcd.DataDir == "" && len(cfg.Etcd.Endpoints) == 0 {
		cfg.Etcd.DataDir = "/var/lib/etcd"
	}

	// Use cfg.NodeName if set, otherwise get that from os.Hostname(). This also makes sure the hostname is lower-cased
	cfg.NodeName = node.GetHostname(cfg.NodeName)

	return nil
}

func defaultAuthorizationModes(authzModes []string, k8sVersion *version.Version) []string {
	if kubeadmutil.IsNodeAuthorizerSupported(k8sVersion) {
		strset := sets.NewString(authzModes...)
		if !strset.Has(authzmodes.ModeNode) {
			return append([]string{authzmodes.ModeNode}, authzModes...)
		}
	}
	return authzModes
}
