package avahi

import (
	"fmt"

	dbus "github.com/godbus/dbus/v5"
)

// An AddressResolver resolves Address to IP addresses
type AddressResolver struct {
	object       dbus.BusObject
	foundChannel chan Address
	closeCh      chan struct{}
}

// AddressResolverNew creates a new AddressResolver
func AddressResolverNew(conn *dbus.Conn, path dbus.ObjectPath) (AddressResolverInterface, error) {
	c := new(AddressResolver)

	c.object = conn.Object("org.freedesktop.Avahi", path)
	c.foundChannel = make(chan Address)
	c.closeCh = make(chan struct{})

	return c, nil
}

var _ AddressResolverInterface = (*AddressResolver)(nil)

func (c *AddressResolver) FoundChannel() chan Address {
	return c.foundChannel
}

func (c *AddressResolver) interfaceForMember(method string) string {
	return fmt.Sprintf("%s.%s", "org.freedesktop.Avahi.AddressResolver", method)
}

func (c *AddressResolver) Free() {
	if c.closeCh != nil {
		close(c.closeCh)
	}
	c.object.Call(c.interfaceForMember("Free"), 0)
}

func (c *AddressResolver) GetObjectPath() dbus.ObjectPath {
	return c.object.Path()
}

func (c *AddressResolver) DispatchSignal(signal *dbus.Signal) error {
	if signal.Name == c.interfaceForMember("Found") {
		var address Address
		err := dbus.Store(signal.Body, &address.Interface, &address.Protocol,
			&address.Aprotocol, &address.Address, &address.Name,
			&address.Flags)
		if err != nil {
			return err
		}

		select {
		case c.foundChannel <- address:
		case <-c.closeCh:
			close(c.foundChannel)
			c.closeCh = nil
		}
	}

	return nil
}
