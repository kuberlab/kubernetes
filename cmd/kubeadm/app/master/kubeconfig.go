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

package master

import (
	"crypto/rsa"
	"crypto/x509"
	"fmt"

	// TODO: "k8s.io/client-go/client/tools/clientcmd/api"
	"encoding/base64"
	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	kubeadmutil "k8s.io/kubernetes/cmd/kubeadm/app/util"
	clientcmdapi "k8s.io/kubernetes/pkg/client/unversioned/clientcmd/api"
	certutil "k8s.io/kubernetes/pkg/util/cert"
)

func CreateCertsAndConfigForClients(clusterName string, cfg kubeadmapi.API, clientNames []string, caKey *rsa.PrivateKey, caCert *x509.Certificate, security kubeadmapi.Security) (map[string]*clientcmdapi.Config, error) {
	var err error

	configs := map[string]*clientcmdapi.Config{}
	for _, client := range clientNames {
		certConfig := certutil.Config{
			CommonName:   client,
			Organization: []string{client},
		}
		keyCert, exist := security.ClientConf[client]

		var keyPem []byte
		var certPem []byte

		if !exist {
			key, err := certutil.NewPrivateKey()
			if err != nil {
				return nil, fmt.Errorf("unable to create private key [%v]", err)
			}
			cert, err := certutil.NewSignedCert(certConfig, key, caCert, caKey)
			if err != nil {
				return nil, fmt.Errorf("<master/kubeconfig> failure while creating %s client certificate - %v", client, err)
			}
			keyPem = certutil.EncodePrivateKeyPEM(key)
			certPem = certutil.EncodeCertPEM(cert)
		} else if len(keyCert.Key) > 1 && len(keyCert.Cert) > 1 {
			fmt.Printf("<master/kubeconfig> Using existing client keys for %v", client)
			if keyPem, err = base64.StdEncoding.DecodeString(keyCert.Key); err != nil {
				return nil, fmt.Errorf("<master/kubeconfig> failure while decoding key pem: %v", err)
			}
			if certPem, err = base64.StdEncoding.DecodeString(keyCert.Cert); err != nil {
				return nil, fmt.Errorf("<master/kubeconfig> failure while decoding cert pem: %v", err)
			}
		}
		server := fmt.Sprintf("https://%s:%d", cfg.AdvertiseAddresses[0], cfg.BindPort)
		conf := kubeadmutil.CreateWithCerts(
			server, clusterName, client, certutil.EncodeCertPEM(caCert), keyPem, certPem,
		)
		configs[client] = conf
	}
	return configs, nil
}
