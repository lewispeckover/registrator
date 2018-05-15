package consul

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/gliderlabs/registrator/bridge"
	consulapi "github.com/hashicorp/consul/api"
)

const DefaultInterval = "10s"

func init() {
	f := new(Factory)
	bridge.Register(f, "consul")
	bridge.Register(f, "consul-unix")
}

func (r *ConsulAdapter) interpolateService(script string, service *bridge.Service) string {
	withExposedIp := strings.Replace(script, "$EXPOSED_IP", service.Origin.ExposedIP, -1)
	withExposedPort := strings.Replace(withExposedIp, "$EXPOSED_PORT", service.Origin.ExposedPort, -1)
	withHostIp := strings.Replace(withExposedPort, "$HOST_IP", service.Origin.HostIP, -1)
	withHostPort := strings.Replace(withHostIp, "$HOST_PORT", service.Origin.HostPort, -1)
	withIp := strings.Replace(withHostPort, "$SERVICE_IP", service.IP, -1)
	withPort := strings.Replace(withIp, "$SERVICE_PORT", string(service.Port), -1)
	return withPort
}

type Factory struct{}

func (f *Factory) New(uri *url.URL) bridge.RegistryAdapter {
	config := consulapi.DefaultConfig()
	if uri.Scheme == "consul-unix" {
		config.Address = strings.TrimPrefix(uri.String(), "consul-")
	} else if uri.Host != "" {
		config.Address = uri.Host
	}
	client, err := consulapi.NewClient(config)
	if err != nil {
		log.Fatal("consul: ", uri.Scheme)
	}
	return &ConsulAdapter{client: client}
}

type ConsulAdapter struct {
	client *consulapi.Client
}

// Ping will try to connect to consul by attempting to retrieve the current leader.
func (r *ConsulAdapter) Ping() error {
	status := r.client.Status()
	leader, err := status.Leader()
	if err != nil {
		return err
	}
	log.Println("consul: current leader ", leader)

	return nil
}

func (r *ConsulAdapter) Register(service *bridge.Service) error {
	registration := new(consulapi.AgentServiceRegistration)
	registration.ID = service.ID
	registration.Name = service.Name
	registration.Port = service.Port
	registration.Tags = service.Tags
	registration.Address = service.IP
	registration.Check = r.buildCheck(service)
	return r.client.Agent().ServiceRegister(registration)
}

func (r *ConsulAdapter) buildCheck(service *bridge.Service) *consulapi.AgentServiceCheck {
	check := new(consulapi.AgentServiceCheck)
	if path := service.Attrs["check_http"]; path != "" {
		check.HTTP = fmt.Sprintf("http://%s:%s%s", service.Origin.ExposedIP, service.Origin.ExposedPort, path)
		if timeout := service.Attrs["check_timeout"]; timeout != "" {
			check.Timeout = timeout
		}
	} else if cmd := service.Attrs["check_cmd"]; cmd != "" {
		check.Args = [3]string{"/bin/sh", "-c", fmt.Sprintf("check-cmd %s %s %s", service.Origin.ContainerID[:12], service.Origin.ExposedPort, cmd))
	} else if script := service.Attrs["check_script"]; script != "" {
		check.Args = [3]string{"/bin/sh", "-c", r.interpolateService(script, service))
	} else if ttl := service.Attrs["check_ttl"]; ttl != "" {
		check.TTL = ttl
	} else if tcp := service.Attrs["check_tcp"]; tcp != "" {
		check.TCP = fmt.Sprintf("%s:%s", service.Origin.ExposedIP, service.Origin.ExposedPort)
		if timeout := service.Attrs["check_timeout"]; timeout != "" {
			check.Timeout = timeout
		}
	} else {
		return nil
	}
	if check.Script != "" || check.HTTP != "" || check.TCP != "" {
		if interval := service.Attrs["check_interval"]; interval != "" {
			check.Interval = interval
		} else {
			check.Interval = DefaultInterval
		}
	}
	if deregister_after := service.Attrs["check_deregister_after"]; deregister_after != "" {
		check.DeregisterCriticalServiceAfter = deregister_after
	}
	return check
}

func (r *ConsulAdapter) Deregister(service *bridge.Service) error {
	return r.client.Agent().ServiceDeregister(service.ID)
}

func (r *ConsulAdapter) Refresh(service *bridge.Service) error {
	return nil
}

func (r *ConsulAdapter) Services() ([]*bridge.Service, error) {
	services, err := r.client.Agent().Services()
	if err != nil {
		return []*bridge.Service{}, err
	}
	out := make([]*bridge.Service, len(services))
	i := 0
	for _, v := range services {
		s := &bridge.Service{
			ID:   v.ID,
			Name: v.Service,
			Port: v.Port,
			Tags: v.Tags,
			IP:   v.Address,
		}
		out[i] = s
		i++
	}
	return out, nil
}
