package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"log"
	"reflect"
	"strings"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"golang.org/x/exp/jsonrpc2"
)

type rpcServiceFunc interface {
	Call(*Remote, string, json.RawMessage) ([]interface{}, error)
}

func callMethod(remote *Remote, methodName string, method reflect.Value, params []json.RawMessage) ([]interface{}, error) {
	methodType := method.Type()
	neededParams := methodType.NumIn()

	if len(params) != neededParams {
		return nil, jsonrpc2.ErrInvalidParams
	}

	var decodedParams []interface{}
	for idx := 0; idx < neededParams; idx++ {
		paramType := methodType.In(idx)

		var paramValue reflect.Value
		if paramType == reflect.TypeFor[spineapi.DeviceRemoteInterface]() {
			// convert between DeviceRemoteInterface and DeviceAddressType
			paramValue = reflect.New(reflect.TypeFor[model.DeviceAddressType]())
		} else if paramType == reflect.TypeFor[spineapi.EntityRemoteInterface]() {
			// convert between EntityRemoteInterface and EntityAddressType
			paramValue = reflect.New(reflect.TypeFor[model.EntityAddressType]())
		} else {
			paramValue = reflect.New(paramType)
		}

		param := paramValue.Interface()
		if err := json.Unmarshal(params[idx], &param); err != nil {
			return nil, jsonrpc2.ErrParse
		}
		decodedParams = append(decodedParams, param)
	}
	log.Printf("decoded: %v(%v)", methodName, decodedParams)

	if len(decodedParams) != neededParams {
		return nil, jsonrpc2.ErrInvalidParams
	}

	methodParams := make([]reflect.Value, neededParams)
	for dstIndex := 0; dstIndex < neededParams; dstIndex++ {
		paramType := methodType.In(dstIndex)
		paramIndex := dstIndex

		if paramType == reflect.TypeFor[spineapi.DeviceRemoteInterface]() {
			// convert between DeviceRemoteInterface and DeviceAddressType
			address, ok := decodedParams[paramIndex].(*model.DeviceAddressType)
			if !ok || address.Device == nil {
				return nil, jsonrpc2.ErrInvalidParams
			}

			deviceInterface := remote.service.LocalDevice().RemoteDeviceForAddress(*address.Device)
			if deviceInterface == nil {
				return nil, jsonrpc2.ErrInvalidParams
			}

			methodParams[dstIndex] = reflect.ValueOf(deviceInterface)
		} else if paramType == reflect.TypeFor[spineapi.EntityRemoteInterface]() {
			// convert between EntityRemoteInterface and EntityAddressType
			address, ok := decodedParams[paramIndex].(*model.EntityAddressType)
			if !ok || address.Device == nil {
				return nil, jsonrpc2.ErrInvalidParams
			}

			deviceInterface := remote.service.LocalDevice().RemoteDeviceForAddress(*address.Device)
			if deviceInterface == nil {
				return nil, jsonrpc2.ErrInvalidParams
			}

			entityInterface := deviceInterface.Entity(address.Entity)
			if entityInterface == nil {
				return nil, jsonrpc2.ErrInvalidParams
			}

			methodParams[dstIndex] = reflect.ValueOf(entityInterface)
		} else if decodedParams[paramIndex] == nil {
			// some parameters are optional and allowed to be nil
			methodParams[dstIndex] = reflect.New(paramType).Elem()
		} else {
			methodParams[dstIndex] = reflect.ValueOf(decodedParams[paramIndex]).Elem()
		}
	}

	output := method.Call(methodParams)

	return transformReturnValues(output), nil
}

type dynamicReceiverProxy struct{}

func (svc dynamicReceiverProxy) Call(remote *Remote, methodName string, params json.RawMessage) ([]interface{}, error) {
	decodedParams := []json.RawMessage{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &decodedParams); err != nil {
			return nil, jsonrpc2.ErrParse
		}
	}
	log.Printf("decoded: %v(%v)", methodName, decodedParams)

	var deviceAddress model.AddressDeviceType
	var entityAddress model.EntityAddressType
	switch {
	case json.Unmarshal(decodedParams[0], &deviceAddress) == nil:
		deviceInterface := remote.service.LocalDevice().RemoteDeviceForAddress(deviceAddress)
		if deviceInterface == nil {
			return nil, jsonrpc2.ErrInvalidParams
		}

		return svc.call(remote, deviceInterface, methodName, decodedParams[1:])
	case json.Unmarshal(decodedParams[0], &entityAddress) == nil:
		deviceInterface := remote.service.LocalDevice().RemoteDeviceForAddress(*entityAddress.Device)
		if deviceInterface == nil {
			return nil, jsonrpc2.ErrInvalidParams
		}

		entityInterface := deviceInterface.Entity(entityAddress.Entity)
		if entityInterface == nil {
			return nil, jsonrpc2.ErrInvalidParams
		}

		return svc.call(remote, entityInterface, methodName, decodedParams[1:])
	default:
		return nil, jsonrpc2.ErrMethodNotFound
	}
}

func (svc dynamicReceiverProxy) call(remote *Remote, rcvr any, methodName string, params []json.RawMessage) ([]interface{}, error) {
	log.Printf("rcvr: %v", reflect.TypeOf(rcvr))
	method := reflect.ValueOf(rcvr).MethodByName(methodName)
	if method.IsZero() {
		return nil, jsonrpc2.ErrMethodNotFound
	}

	return callMethod(remote, methodName, method, params)
}

type staticReceiverProxy struct {
	name   string
	rcvr   reflect.Value
	typ    reflect.Type
	method map[string]reflect.Value
}

func newStaticReceiverProxy(rcvr any, name string, useName bool) (*staticReceiverProxy, error) {
	c := new(staticReceiverProxy)
	c.typ = reflect.TypeOf(rcvr)
	c.rcvr = reflect.ValueOf(rcvr)
	sname := name
	if !useName {
		sname = reflect.Indirect(c.rcvr).Type().Name()
	}
	if sname == "" {
		s := "rpc.Register: no service name for type " + c.typ.String()
		log.Print(s)
		return nil, errors.New(s)
	}
	if !useName && !token.IsExported(sname) {
		s := "rpc.Register: type " + sname + " is not exported"
		log.Print(s)
		return nil, errors.New(s)
	}
	sname = strings.ToLower(sname)
	c.name = sname

	c.method = make(map[string]reflect.Value)
	for m := 0; m < c.typ.NumMethod(); m++ {
		method := c.typ.Method(m)
		mtype := method.Type
		mname := method.Name

		// Method must be exported
		if !method.IsExported() {
			continue
		}

		// all (non-receiver) arguments must be builtin or exported
		for i := 1; i < mtype.NumIn(); i++ {
			argType := mtype.In(i)
			if !isExportedOrBuiltinType(argType) {
				panic(fmt.Sprintf("UseCaseProxy.Register: argument type of method %q is not exported: %q\n", mname, argType))
			}
			continue
		}
		for i := 1; i < mtype.NumOut(); i++ {
			argType := mtype.Out(i)
			if !isExportedOrBuiltinType(argType) {
				panic(fmt.Sprintf("UseCaseProxy.Register: return type of method %q is not exported: %q\n", mname, argType))
			}
			continue
		}

		log.Printf("registering method %s/%s", sname, mname)
		// bind receiver into method
		c.method[mname] = reflect.ValueOf(rcvr).Method(m)
	}

	return c, nil
}

func (svc *staticReceiverProxy) Call(remote *Remote, methodName string, params json.RawMessage) ([]interface{}, error) {
	method, found := svc.method[methodName]
	if !found {
		return nil, jsonrpc2.ErrNotHandled
	}
	splitParams := []json.RawMessage{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &splitParams); err != nil {
			log.Printf("%v", err)
			return nil, jsonrpc2.ErrParse
		}
	}

	return callMethod(remote, methodName, method, splitParams)

}
