package yaml

import (
	"os"

	"gopkg.in/yaml.v3"

	"minik8s/internal/dns"
)

func LoadDNSFromFile(path string) (*dns.DNS, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadDNSFromYAML(data)
}

func LoadDNSFromYAML(data []byte) (*dns.DNS, error) {
	var d dns.DNS
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	if err := DefaultAndValidateDNS(&d); err != nil {
		return nil, err
	}
	return &d, nil
}
