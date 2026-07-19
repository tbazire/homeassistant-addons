package avahi

import (
	"fmt"

	dbus "github.com/godbus/dbus/v5"
)

// A ServiceBrowser browses for mDNS services
type ServiceBrowser struct {
	object        dbus.BusObject
	addChannel    chan Service
	removeChannel chan Service
}

// ServiceBrowserNew creates a new browser for mDNS records
func ServiceBrowserNew(addChan, removeChan chan Service, conn *dbus.Conn, path dbus.ObjectPath) (ServiceBrowserInterface, error) {
	c := new(ServiceBrowser)

	c.object = conn.Object("org.freedesktop.Avahi", path)
	c.addChannel = addChan
	c.removeChannel = removeChan

	return c, nil
}

var _ ServiceBrowserInterface = (*ServiceBrowser)(nil)

func (c *ServiceBrowser) interfaceForMember(method string) string {
	return fmt.Sprintf("%s.%s", "org.freedesktop.Avahi.ServiceBrowser", method)
}

func (c *ServiceBrowser) Free() {
	c.object.Call(c.interfaceForMember("Free"), 0)
}

func (c *ServiceBrowser) GetObjectPath() dbus.ObjectPath {
	return c.object.Path()
}

func (c *ServiceBrowser) DispatchSignal(signal *dbus.Signal) error {
	if signal.Name == c.interfaceForMember("ItemNew") || signal.Name == c.interfaceForMember("ItemRemove") {
		var service Service
		err := dbus.Store(signal.Body, &service.Interface, &service.Protocol, &service.Name, &service.Type, &service.Domain, &service.Flags)
		if err != nil {
			return err
		}

		if signal.Name == c.interfaceForMember("ItemNew") {
			if c.addChannel != nil {
				c.addChannel <- service
			}
		} else {
			if c.removeChannel != nil {
				c.removeChannel <- service
			}
		}
	}

	return nil
}
