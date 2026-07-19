package avahi

import (
	"fmt"

	dbus "github.com/godbus/dbus/v5"
)

// A RecordBrowser is a browser for mDNS records
type RecordBrowser struct {
	object        dbus.BusObject
	addChannel    chan Record
	removeChannel chan Record
	closeCh       chan struct{}
}

// RecordBrowserNew creates a new mDNS record browser
func RecordBrowserNew(conn *dbus.Conn, path dbus.ObjectPath) (RecordBrowserInterface, error) {
	c := new(RecordBrowser)

	c.object = conn.Object("org.freedesktop.Avahi", path)
	c.addChannel = make(chan Record)
	c.removeChannel = make(chan Record)
	c.closeCh = make(chan struct{})

	return c, nil
}

var _ HostNameResolverInterface = (*HostNameResolver)(nil)

func (c *RecordBrowser) AddChannel() chan Record {
	return c.addChannel
}

func (c *RecordBrowser) RemoveChannel() chan Record {
	return c.removeChannel
}

func (c *RecordBrowser) interfaceForMember(method string) string {
	return fmt.Sprintf("%s.%s", "org.freedesktop.Avahi.RecordBrowser", method)
}

func (c *RecordBrowser) Free() {
	close(c.closeCh)
	close(c.addChannel)
	close(c.removeChannel)
	c.object.Call(c.interfaceForMember("Free"), 0)
}

func (c *RecordBrowser) GetObjectPath() dbus.ObjectPath {
	return c.object.Path()
}

func (c *RecordBrowser) DispatchSignal(signal *dbus.Signal) error {
	if signal.Name == c.interfaceForMember("ItemNew") || signal.Name == c.interfaceForMember("ItemRemove") {
		var record Record
		err := dbus.Store(signal.Body, &record.Interface, &record.Protocol, &record.Name,
			&record.Class, &record.Type, &record.Rdata, &record.Flags)
		if err != nil {
			return err
		}

		if signal.Name == c.interfaceForMember("ItemNew") {
			select {
			case c.addChannel <- record:
			case <-c.closeCh:
			}
		} else {
			select {
			case c.removeChannel <- record:
			case <-c.closeCh:
			}
		}
	}

	return nil
}
