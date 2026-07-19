package avahi

import (
	"fmt"

	dbus "github.com/godbus/dbus/v5"
)

// A ServiceTypeBrowser is used to browser the mDNS network for services of a specific type
type ServiceTypeBrowser struct {
	object        dbus.BusObject
	addChannel    chan ServiceType
	removeChannel chan ServiceType
	closeCh       chan struct{}
}

// ServiceTypeBrowserNew creates a new browser for mDNS service types
func ServiceTypeBrowserNew(conn *dbus.Conn, path dbus.ObjectPath) (ServiceTypeBrowserInterface, error) {
	c := new(ServiceTypeBrowser)

	c.object = conn.Object("org.freedesktop.Avahi", path)
	c.addChannel = make(chan ServiceType)
	c.removeChannel = make(chan ServiceType)
	c.closeCh = make(chan struct{})

	return c, nil
}

var _ ServiceTypeBrowserInterface = (*ServiceTypeBrowser)(nil)

func (c *ServiceTypeBrowser) AddChannel() chan ServiceType {
	return c.addChannel
}

func (c *ServiceTypeBrowser) RemoveChannel() chan ServiceType {
	return c.removeChannel
}

func (c *ServiceTypeBrowser) interfaceForMember(method string) string {
	return fmt.Sprintf("%s.%s", "org.freedesktop.Avahi.ServiceTypeBrowser", method)
}

func (c *ServiceTypeBrowser) Free() {
	if c.closeCh != nil {
		close(c.closeCh)
	}
	c.object.Call(c.interfaceForMember("Free"), 0)
}

func (c *ServiceTypeBrowser) GetObjectPath() dbus.ObjectPath {
	return c.object.Path()
}

func (c *ServiceTypeBrowser) DispatchSignal(signal *dbus.Signal) error {
	if signal.Name == c.interfaceForMember("ItemNew") || signal.Name == c.interfaceForMember("ItemRemove") {
		var serviceType ServiceType
		err := dbus.Store(signal.Body, &serviceType.Interface, &serviceType.Protocol, &serviceType.Type, &serviceType.Domain, &serviceType.Flags)
		if err != nil {
			return err
		}

		if signal.Name == c.interfaceForMember("ItemNew") {
			select {
			case c.addChannel <- serviceType:
			case <-c.closeCh:
				close(c.addChannel)
				close(c.removeChannel)
				c.closeCh = nil
			}
		} else {
			select {
			case c.removeChannel <- serviceType:
			case <-c.closeCh:
				close(c.addChannel)
				close(c.removeChannel)
				c.closeCh = nil
			}
		}
	}

	return nil
}
