package avahi

import dbus "github.com/godbus/dbus/v5"

//go:generate mockery

type EventCB func(event Event)

type ServerInterface interface {
	// Setup the server
	Setup(eventCB EventCB) error
	// Start the server
	Start()
	// Close the connection to a dbus server
	Shutdown()
	// EntryGroupNew returns a new and empty EntryGroup
	EntryGroupNew() (EntryGroupInterface, error)
	// EntryGroupFree frees an entry group and releases its resources on the service
	EntryGroupFree(r EntryGroupInterface)
	// ResolveHostName ...
	ResolveHostName(iface, protocol int32, name string, aprotocol int32, flags uint32) (reply HostName, err error)
	// ResolveAddress ...
	ResolveAddress(iface, protocol int32, address string, flags uint32) (reply Address, err error)
	// ResolveService ...
	ResolveService(iface, protocol int32, name, serviceType, domain string, aprotocol int32, flags uint32) (reply Service, err error)
	// DomainBrowserNew ...
	DomainBrowserNew(iface, protocol int32, domain string, btype int32, flags uint32) (DomainBrowserInterface, error)
	// DomainBrowserFree ...
	DomainBrowserFree(r DomainBrowserInterface)
	// ServiceTypeBrowserNew ...
	ServiceTypeBrowserNew(iface, protocol int32, domain string, flags uint32) (ServiceTypeBrowserInterface, error)
	// ServiceTypeBrowserFree ...
	ServiceTypeBrowserFree(r ServiceTypeBrowserInterface)
	// ServiceBrowserNew ...
	ServiceBrowserNew(addChan, removeChan chan Service, iface, protocol int32, serviceType string, domain string, flags uint32) (ServiceBrowserInterface, error)
	// ServiceBrowserFree ...
	ServiceBrowserFree(r ServiceBrowserInterface)
	// ServiceResolverNew ...
	ServiceResolverNew(iface, protocol int32, name, serviceType, domain string, aprotocol int32, flags uint32) (ServiceResolverInterface, error)
	// ServiceResolverFree ...
	ServiceResolverFree(r ServiceResolverInterface)
	// HostNameResolverNew ...
	HostNameResolverNew(iface, protocol int32, name string, aprotocol int32, flags uint32) (HostNameResolverInterface, error)
	// AddressResolverNew ...
	AddressResolverNew(iface, protocol int32, address string, flags uint32) (AddressResolverInterface, error)
	// AddressResolverFree ...
	AddressResolverFree(r AddressResolverInterface)
	// RecordBrowserNew ...
	RecordBrowserNew(iface, protocol int32, name string, class uint16, recordType uint16, flags uint32) (RecordBrowserInterface, error)
	// RecordBrowserFree ...
	RecordBrowserFree(r RecordBrowserInterface)
	// GetAPIVersion ...
	GetAPIVersion() (int32, error)
	// GetAlternativeHostName ...
	GetAlternativeHostName(name string) (string, error)
	// GetAlternativeServiceName ...
	GetAlternativeServiceName(name string) (string, error)
	// GetDomainName ...
	GetDomainName() (string, error)
	// GetHostName ...
	GetHostName() (string, error)
	// GetHostNameFqdn ...
	GetHostNameFqdn() (string, error)
	// GetLocalServiceCookie ...
	GetLocalServiceCookie() (int32, error)
	// GetNetworkInterfaceIndexByName -...
	GetNetworkInterfaceIndexByName(name string) (int32, error)
	// GetNetworkInterfaceNameByIndex ...
	GetNetworkInterfaceNameByIndex(index int32) (string, error)
	// GetState ...
	GetState() (int32, error)
	// GetVersionString ...
	GetVersionString() (string, error)
	// IsNSSSupportAvailable ...
	IsNSSSupportAvailable() (bool, error)
	// SetServerName ...
	SetServerName(name string) error
}

type EntryGroupInterface interface {
	SignalEmitter

	Commit() error
	Reset() error
	GetState() (int32, error)
	IsEmpty() (bool, error)

	// AddService adds a service. Takes a list of TXT record strings as last arguments.
	// Please note that this service is not announced on the network before Commit() is called.
	AddService(iface, protocol int32, flags uint32, name, serviceType, domain, host string, port uint16, txt [][]byte) error

	// AddServiceSubtype adds a subtype for a service. The service should already be existent in the entry group.
	// You may add as many subtypes for a service as you wish.
	AddServiceSubtype(iface, protocol int32, flags uint32, name, serviceType, domain, subtype string) error

	// UpdateServiceTxt apdates a TXT record for an existing service.
	// The service should already be existent in the entry group.
	UpdateServiceTxt(iface, protocol int32, flags uint32, name, serviceType, domain string, txt [][]byte) error

	// AddAddress add a host/address pair to the entry group
	AddAddress(iface, protocol int32, flags uint32, name, address string) error

	// AddRecord adds an arbitrary record. I hope you know what you do.
	AddRecord(iface, protocol int32, flags uint32, name string, class, recordType uint16, ttl uint32, rdata []byte) error
}

type SignalEmitter interface {
	DispatchSignal(signal *dbus.Signal) error
	GetObjectPath() dbus.ObjectPath
	Free()
}

type DomainBrowserInterface interface {
	SignalEmitter
}

type ServiceTypeBrowserInterface interface {
	SignalEmitter

	AddChannel() chan ServiceType
	RemoveChannel() chan ServiceType
}

type ServiceBrowserInterface interface {
	SignalEmitter
}

type ServiceResolverInterface interface {
	SignalEmitter

	FoundChannel() chan Service
}

type HostNameResolverInterface interface {
	SignalEmitter

	FoundChannel() chan HostName
}

type AddressResolverInterface interface {
	SignalEmitter

	FoundChannel() chan Address
}

type RecordBrowserInterface interface {
	SignalEmitter

	AddChannel() chan Record
	RemoveChannel() chan Record
}
