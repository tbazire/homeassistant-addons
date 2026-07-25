package avahi

import (
	"fmt"

	dbus "github.com/godbus/dbus/v5"
)

// A HostNameResolver can resolve host names
type HostNameResolver struct {
	object       dbus.BusObject
	foundChannel chan HostName
	closeCh      chan struct{}
}

// HostNameResolverNew returns a new HostNameResolver
func HostNameResolverNew(conn *dbus.Conn, path dbus.ObjectPath) (HostNameResolverInterface, error) {
	c := new(HostNameResolver)

	c.object = conn.Object("org.freedesktop.Avahi", path)
	c.foundChannel = make(chan HostName)
	c.closeCh = make(chan struct{})

	return c, nil
}

var _ HostNameResolverInterface = (*HostNameResolver)(nil)

func (c *HostNameResolver) FoundChannel() chan HostName {
	return c.foundChannel
}

func (c *HostNameResolver) interfaceForMember(method string) string {
	return fmt.Sprintf("%s.%s", "org.freedesktop.Avahi.HostNameResolver", method)
}

func (c *HostNameResolver) Free() {
	if c.closeCh != nil {
		close(c.closeCh)
	}
	c.object.Call(c.interfaceForMember("Free"), 0)
}

func (c *HostNameResolver) GetObjectPath() dbus.ObjectPath {
	return c.object.Path()
}

func (c *HostNameResolver) DispatchSignal(signal *dbus.Signal) error {
	if signal.Name == c.interfaceForMember("Found") {
		var hostName HostName
		err := dbus.Store(signal.Body, &hostName.Interface, &hostName.Protocol,
			&hostName.Name, &hostName.Aprotocol, &hostName.Address,
			&hostName.Flags)
		if err != nil {
			return err
		}

		select {
		case c.foundChannel <- hostName:
		case <-c.closeCh:
			close(c.foundChannel)
			c.closeCh = nil
		}
	}

	return nil
}
