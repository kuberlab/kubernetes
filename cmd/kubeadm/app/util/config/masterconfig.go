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
	"io/ioutil"
	"net"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	netutil "k8s.io/apimachinery/pkg/util/net"
	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	kubeadmapiext "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/v1alpha1"
	"k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/validation"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/controlplane"
	kubeadmutil "k8s.io/kubernetes/cmd/kubeadm/app/util"
	tokenutil "k8s.io/kubernetes/cmd/kubeadm/app/util/token"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	"k8s.io/kubernetes/pkg/cloudprovider"
	_ "k8s.io/kubernetes/pkg/cloudprovider/providers"
	"k8s.io/kubernetes/pkg/util/node"
	"k8s.io/kubernetes/pkg/util/version"
)

// SetInitDynamicDefaults checks and sets conifugration values for Master node
func SetInitDynamicDefaults(cfg *kubeadmapi.MasterConfiguration) error {

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
		if _, err = os.Stat(controlplane.DefaultCloudConfigPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		config, err = os.Open(controlplane.DefaultCloudConfigPath)
		// Return error only if specified file exists and error relates to read.
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		cloudSupport, err := cloudprovider.GetCloudProvider(cfg.CloudProvider, config)
		if err != nil {
			fmt.Printf("[init] WARNING: Failed to get support for cloudprovider '%s': %v\n", cfg.CloudProvider, err)
		} else {
			if instances, ok := cloudSupport.Instances(); ok {
				if name, err := instances.CurrentNodeName(node.GetHostname("")); err != nil {
					fmt.Printf("[init] WARNING: Failed to get node name for cloud provider '%s': %v\n", cfg.CloudProvider, err)
				} else {
					cfg.HostnameOverride = string(name)
					fmt.Printf("[init] Using Kubernetes nodename %s for cloud provider: %s\n",
						cfg.HostnameOverride, cfg.CloudProvider)
				}
			}
		}
	}

	// Resolve possible version labels and validate version string
	err = NormalizeKubernetesVersion(cfg)
	if err != nil {
		return err
	}

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

	cfg.NodeName = node.GetHostname(cfg.NodeName)

	return nil
}

// TryLoadMasterConfiguration tries to loads a Master configuration from the given file (if defined)
func TryLoadMasterConfiguration(cfgPath string, cfg *kubeadmapiext.MasterConfiguration) error {

	if cfgPath != "" {
		b, err := ioutil.ReadFile(cfgPath)
		if err != nil {
			return fmt.Errorf("unable to read config from %q [%v]", cfgPath, err)
		}
		if err := runtime.DecodeInto(legacyscheme.Codecs.UniversalDecoder(), b, cfg); err != nil {
			return fmt.Errorf("unable to decode config from %q [%v]", cfgPath, err)
		}
	}

	return nil
}

// ConfigFileAndDefaultsToInternalConfig takes a path to a config file and a versioned configuration that can serve as the default config
// If cfgPath is specified, defaultversionedcfg will always get overridden. Otherwise, the default config (often populated by flags) will be used.
// Then the external, versioned configuration is defaulted and converted to the internal type.
// Right thereafter, the configuration is defaulted again with dynamic values (like IP addresses of a machine, etc)
// Lastly, the internal config is validated and returned.
func ConfigFileAndDefaultsToInternalConfig(cfgPath string, defaultversionedcfg *kubeadmapiext.MasterConfiguration) (*kubeadmapi.MasterConfiguration, error) {
	internalcfg := &kubeadmapi.MasterConfiguration{}

	// Loads configuration from config file, if provided
	// Nb. --config overrides command line flags
	if err := TryLoadMasterConfiguration(cfgPath, defaultversionedcfg); err != nil {
		return nil, err
	}

	// Takes passed flags into account; the defaulting is executed once again enforcing assignement of
	// static default values to cfg only for values not provided with flags
	legacyscheme.Scheme.Default(defaultversionedcfg)
	legacyscheme.Scheme.Convert(defaultversionedcfg, internalcfg, nil)
	// Applies dynamic defaults to settings not provided with flags
	if err := SetInitDynamicDefaults(internalcfg); err != nil {
		return nil, err
	}

	// Validates cfg (flags/configs + defaults + dynamic defaults)
	if err := validation.ValidateMasterConfiguration(internalcfg).ToAggregate(); err != nil {
		return nil, err
	}
	return internalcfg, nil
}

// NormalizeKubernetesVersion resolves version labels, sets alternative
// image registry if requested for CI builds, and validates minimal
// version that kubeadm supports.
func NormalizeKubernetesVersion(cfg *kubeadmapi.MasterConfiguration) error {
	// Requested version is automatic CI build, thus use KubernetesCI Image Repository for core images
	if kubeadmutil.KubernetesIsCIVersion(cfg.KubernetesVersion) {
		cfg.CIImageRepository = kubeadmconstants.DefaultCIImageRepository
	}

	// Parse and validate the version argument and resolve possible CI version labels
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
	return nil
}
