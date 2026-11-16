package aiops

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Service 是服务目录中的一个条目。
type Service struct {
	Owner     string `yaml:"owner" json:"owner"`
	Team      string `yaml:"team" json:"team"`
	Runbook   string `yaml:"runbook" json:"runbook"`
	Dashboard string `yaml:"dashboard" json:"dashboard"`
	Repo      string `yaml:"repo" json:"repo"`
	Namespace string `yaml:"namespace" json:"namespace"`
}

// Catalog 是服务目录。
type Catalog struct {
	Services map[string]Service `yaml:"services" json:"services"`
}

// LoadCatalog 读取 services.yaml；文件不存在时返回空目录。
func LoadCatalog(path string) (*Catalog, error) {
	c := &Catalog{Services: map[string]Service{}}
	if path == "" {
		return c, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(raw, c); err != nil {
		return nil, err
	}
	if c.Services == nil {
		c.Services = map[string]Service{}
	}
	return c, nil
}

// Lookup 按服务名查找，支持大小写不敏感。
func (c *Catalog) Lookup(service string) (Service, bool) {
	if c == nil || service == "" {
		return Service{}, false
	}
	if svc, ok := c.Services[service]; ok {
		return svc, true
	}
	for key, svc := range c.Services {
		if strings.EqualFold(key, service) {
			return svc, true
		}
	}
	return Service{}, false
}
