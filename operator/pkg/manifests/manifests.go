package manifests

import (
	"bytes"
	"embed"
	"io"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	DNSNameResolverCRDAsset = "assets/0000_70_dnsnameresolver_00-techpreview.crd.yaml"
)

//go:embed assets
var content embed.FS

// MustAsset returns the bytes for the named assert.
func MustAsset(asset string) []byte {
	b, err := content.ReadFile(asset)
	if err != nil {
		panic(err)
	}
	return b
}

func MustAssetReader(asset string) io.Reader {
	return bytes.NewReader(MustAsset(asset))
}

func NewCustomResourceDefinition(manifest io.Reader) (*apiextensionsv1.CustomResourceDefinition, error) {
	o := apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&o); err != nil {
		return nil, err
	}

	return &o, nil
}

func DNSNameResolverCRD() *apiextensionsv1.CustomResourceDefinition {
	crd, err := NewCustomResourceDefinition(MustAssetReader(DNSNameResolverCRDAsset))
	if err != nil {
		panic(err)
	}
	return crd
}
