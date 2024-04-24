# DNSNameResolver Operator
The DNSNameResolver Operator can be used to test the unmanaged DNSNameResolver conroller.

## Description
The DNSNameResolver controller sends DNS name lookup requests to maximum 5 random CoreDNS
pods whenever the TTL of the IP addresses corresponding to a DNS name expires. The DNS name
lookups will be handled by the ocp-dnsnameresolver CoreDNS plugin and any new IP address
corresponding to the DNS name will be added to the status of the DNSNameResolver CR. The
controller also removes the stale IP addresses from the status of the DNSNameResolver CRs
after a grace period.

To test the unmanaged DNSNameResolver controller, the operator runs another controller which
watches the DNSNameResolver CRD and starts the DNSNameResolver controller.

Kindly provide correct values for the following arguments using the `args` field of the `manager`
container in the [`manager.yaml`](./config/manager/manager.yaml) when deploying the operator:
- `coredns-namespace`
- `coredns-service-name`
- `dns-name-resolver-namespace`

## Getting Started

### Prerequisites
- go version v1.19.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified. 
And it is required to have access to pull the image from the working environment. 
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to login as cluster-admin.

### To Uninstall
```

**UnDeploy the operator from the cluster:**

```sh
make undeploy

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

## License

Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

