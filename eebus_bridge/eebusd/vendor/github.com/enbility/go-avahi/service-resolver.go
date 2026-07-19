package avahi

import (
	"fmt"

	dbus "github.com/godbus/dbus/v5"
)

// A ServiceResolver resolves mDNS services to IP addresses
type ServiceResolver struct {
	object       dbus.BusObject
	foundChannel chan Service
	closeCh      chan struct{}
}

// ServiceResolverNew returns a new mDNS service resolver
func ServiceResolverNew(conn *dbus.Conn, path dbus.ObjectPath) (ServiceResolverInterface, error) {
	c := new(ServiceResolver)

	c.object = conn.Object("org.freedesktop.Avahi", path)
	c.foundChannel = make(chan Service)
	c.closeCh = make(chan struct{})

	return c, nil
}

var _ ServiceResolverInterface = (*ServiceResolver)(nil)

func (c *ServiceResolver) FoundChannel() chan Service {
	return c.foundChannel
}

func (c *ServiceResolver) interfaceForMember(method string) string {
	return fmt.Sprintf("%s.%s", "org.freedesktop.Avahi.ServiceResolver", method)
}

func (c *ServiceResolver) Free() {
	if c.closeCh != nil {
		close(c.closeCh)
	}
	c.object.Call(c.interfaceForMember("Free"), 0)
}

func (c *ServiceResolver) GetObjectPath() dbus.ObjectPath {
	return c.object.Path()
}

func (c *ServiceResolver) DispatchSignal(signal *dbus.Signal) error {
	if signal.Name == c.interfaceForMember("Found") {
		var service Service
		err := dbus.Store(signal.Body, &service.Interface, &service.Protocol,
			&service.Name, &service.Type, &service.Domain, &service.Host,
			&service.Aprotocol, &service.Address, &service.Port,
			&service.Txt, &service.Flags)
		if err != nil {
			return err
		}

		select {
		case c.foundChannel <- service:
		case <-c.closeCh:
			close(c.foundChannel)
			c.closeCh = nil
		}
	}

	return nil
}
