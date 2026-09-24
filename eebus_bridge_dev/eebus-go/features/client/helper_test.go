package client

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	shipapi "github.com/enbility/ship-go/api"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/spine"
	"github.com/enbility/spine-go/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type featureFunctions struct {
	featureType model.FeatureTypeType
	functions   []model.FunctionType
	partial     bool
}

func commandFromMessage(t *testing.T, message []byte) model.CmdType {
	t.Helper()
	require.NotEmpty(t, message)
	var datagram model.Datagram
	require.NoError(t, json.Unmarshal(message, &datagram))
	require.Len(t, datagram.Datagram.Payload.Cmd, 1)
	return datagram.Datagram.Payload.Cmd[0]
}

func TestPrepareListWriteFailsClosedWhenCacheMergeFails(t *testing.T) {
	function := model.FunctionTypeSetpointListData
	remote := spinemocks.NewFeatureRemoteInterface(t)
	remote.EXPECT().Operations().Return(map[model.FunctionType]spineapi.OperationsInterface{
		function: spine.NewOperations(true, false, true, false),
	})
	remote.EXPECT().UpdateData(false, function, mock.Anything, mock.Anything, mock.Anything).Return(
		nil,
		model.NewErrorType(model.ErrorNumberTypeCommandRejected, "cache unavailable"),
	)

	data := []model.SetpointDataType{{
		SetpointId: util.Ptr(model.SetpointIdType(1)),
		Value:      model.NewScaledNumberType(20),
	}}
	merged, filters, err := prepareListWrite[model.SetpointDataType](
		remote,
		function,
		&model.SetpointListDataType{SetpointData: data},
		data,
	)
	require.Error(t, err)
	assert.True(t, errors.Is(err, eebusapi.ErrDataNotAvailable))
	assert.Nil(t, merged)
	assert.Nil(t, filters)
}

type WriteMessageHandler struct {
	sentMessages [][]byte

	mux sync.Mutex
}

var _ shipapi.ShipConnectionDataWriterInterface = (*WriteMessageHandler)(nil)

func (t *WriteMessageHandler) WriteShipMessageWithPayload(message []byte) {
	t.mux.Lock()
	defer t.mux.Unlock()

	t.sentMessages = append(t.sentMessages, message)
}

func (t *WriteMessageHandler) LastMessage() []byte {
	t.mux.Lock()
	defer t.mux.Unlock()

	if len(t.sentMessages) == 0 {
		return nil
	}

	return t.sentMessages[len(t.sentMessages)-1]
}

func (t *WriteMessageHandler) MessageWithReference(msgCounterReference *model.MsgCounterType) []byte {
	t.mux.Lock()
	defer t.mux.Unlock()

	var datagram model.Datagram

	for _, msg := range t.sentMessages {
		if err := json.Unmarshal(msg, &datagram); err != nil {
			return nil
		}
		if datagram.Datagram.Header.MsgCounterReference == nil {
			continue
		}
		if uint(*datagram.Datagram.Header.MsgCounterReference) != uint(*msgCounterReference) {
			continue
		}
		if datagram.Datagram.Payload.Cmd[0].ResultData != nil {
			continue
		}

		return msg
	}

	return nil
}

func (t *WriteMessageHandler) ResultWithReference(msgCounterReference *model.MsgCounterType) []byte {
	t.mux.Lock()
	defer t.mux.Unlock()

	var datagram model.Datagram

	for _, msg := range t.sentMessages {
		if err := json.Unmarshal(msg, &datagram); err != nil {
			return nil
		}
		if datagram.Datagram.Header.MsgCounterReference == nil {
			continue
		}
		if uint(*datagram.Datagram.Header.MsgCounterReference) != uint(*msgCounterReference) {
			continue
		}
		if datagram.Datagram.Payload.Cmd[0].ResultData == nil {
			continue
		}

		return msg
	}

	return nil
}

func setupFeatures(
	t assert.TestingT,
	dataCon shipapi.ShipConnectionDataWriterInterface,
	featureFunctions []featureFunctions) (spineapi.EntityLocalInterface, spineapi.EntityRemoteInterface) {
	localDevice := spine.NewDeviceLocal("TestBrandName", "TestDeviceModel", "TestSerialNumber", "TestDeviceCode",
		"TestDeviceAddress", model.DeviceTypeTypeEnergyManagementSystem, model.NetworkManagementFeatureSetTypeSmart)
	localEntity := spine.NewEntityLocal(localDevice, model.EntityTypeTypeCEM, spine.NewAddressEntityType([]uint{1}), time.Second*4)

	for i, item := range featureFunctions {
		f := spine.NewFeatureLocal(uint(i+1), localEntity, item.featureType, model.RoleTypeClient)
		localEntity.AddFeature(f)
	}

	localDevice.AddEntity(localEntity)

	remoteDeviceName := "remoteDevice"
	sender := spine.NewSender(dataCon)
	remoteDevice := spine.NewDeviceRemote(localDevice, "test", sender)
	data := &model.NodeManagementDetailedDiscoveryDataType{
		DeviceInformation: &model.NodeManagementDetailedDiscoveryDeviceInformationType{
			Description: &model.NetworkManagementDeviceDescriptionDataType{
				DeviceAddress: &model.DeviceAddressType{
					Device: util.Ptr(model.AddressDeviceType(remoteDeviceName)),
				},
			},
		},
		EntityInformation: []model.NodeManagementDetailedDiscoveryEntityInformationType{
			{
				Description: &model.NetworkManagementEntityDescriptionDataType{
					EntityAddress: &model.EntityAddressType{
						Device: util.Ptr(model.AddressDeviceType(remoteDeviceName)),
						Entity: []model.AddressEntityType{1},
					},
					EntityType: util.Ptr(model.EntityTypeTypeEVSE),
				},
			},
		},
	}

	var features []model.NodeManagementDetailedDiscoveryFeatureInformationType
	for i, item := range featureFunctions {
		featureI := model.NodeManagementDetailedDiscoveryFeatureInformationType{
			Description: &model.NetworkManagementFeatureDescriptionDataType{
				FeatureAddress: &model.FeatureAddressType{
					Device:  util.Ptr(model.AddressDeviceType(remoteDeviceName)),
					Entity:  []model.AddressEntityType{1},
					Feature: util.Ptr(model.AddressFeatureType(i + 1)),
				},
				FeatureType: util.Ptr(item.featureType),
				Role:        util.Ptr(model.RoleTypeServer),
			},
		}
		var supportedFcts []model.FunctionPropertyType
		for _, function := range item.functions {
			read := &model.PossibleOperationsReadType{}
			write := &model.PossibleOperationsWriteType{}
			if item.partial {
				read = &model.PossibleOperationsReadType{
					Partial: &model.ElementTagType{},
				}
				write = &model.PossibleOperationsWriteType{
					Partial: &model.ElementTagType{},
				}
			}

			supportedFct := model.FunctionPropertyType{
				Function: util.Ptr(function),
				PossibleOperations: &model.PossibleOperationsType{
					Read:  read,
					Write: write,
				},
			}

			supportedFcts = append(supportedFcts, supportedFct)
		}
		featureI.Description.SupportedFunction = supportedFcts
		features = append(features, featureI)
	}
	data.FeatureInformation = features

	remoteEntities, err := remoteDevice.AddEntityAndFeatures(true, data, nil)
	assert.Nil(t, err)
	assert.NotNil(t, remoteEntities)
	assert.NotEqual(t, 0, len(remoteEntities))
	remoteDevice.UpdateDevice(data.DeviceInformation.Description)

	localDevice.AddRemoteDeviceForSki("test", remoteDevice)

	return localEntity, remoteEntities[0]
}
