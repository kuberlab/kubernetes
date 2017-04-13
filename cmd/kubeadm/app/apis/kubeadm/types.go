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

package kubeadm

import "k8s.io/kubernetes/pkg/api/unversioned"

type EnvParams struct {
	KubernetesDir     string
	HostPKIPath       string
	HostEtcdPath      string
	HyperkubeImage    string
	DiscoveryImage    string
	EtcdImage         string
	ComponentLoglevel string
}

type MasterConfiguration struct {
	unversioned.TypeMeta

	Secrets           Secrets
	API               API
	Discovery         Discovery
	Etcd              Etcd
	Networking        Networking
	Security          Security
	KubernetesVersion string
	CloudProvider     string
	ClusterName       string
	DNSRequired       bool
}

type Security struct {
	CAKeyPem         string
	CACertPem        string
	APIServerKeyPem  string
	APIServerCertPem string
	SAKeyPem         string
	ClientConf       map[string]ClientKeyCert
	Password         string
}

type ClientKeyCert struct {
	Key  string
	Cert string
}

type API struct {
	AdvertiseAddresses []string
	ExternalDNSNames   []string
	MasterDNSName      string
	BindPort           int32
	MasterCount        int
}

type Discovery struct {
	BindPort int32
}

type Networking struct {
	ServiceSubnet string
	PodSubnet     string
	DNSDomain     string
}

type Etcd struct {
	Endpoints []string
	Discovery string
	CAFile    string
	CertFile  string
	KeyFile   string
}

type Secrets struct {
	GivenToken  string // dot-separated `<TokenID>.<Token>` set by the user
	TokenID     string // optional on master side, will be generated if not specified
	Token       []byte // optional on master side, will be generated if not specified
	BearerToken string // set based on Token
}

type NodeConfiguration struct {
	unversioned.TypeMeta

	MasterAddresses []string
	Secrets         Secrets
	APIPort         int32
	DiscoveryPort   int32
	ClusterName     string
}

// ClusterInfo TODO add description
type ClusterInfo struct {
	unversioned.TypeMeta
	// TODO(phase1+) this may become simply `api.Config`
	CertificateAuthorities []string `json:"certificateAuthorities"`
	Endpoints              []string `json:"endpoints"`
}
