# eebus-go Architecture

This document provides a comprehensive overview of the architecture of the eebus-go library, which implements the EEBUS protocol stack for energy management systems in Go.

## Overview

The eebus-go library is a Go implementation of the EEBUS standard, providing a foundation for implementing energy management use cases. It builds upon two core protocol implementations:

- **SHIP** (Smart Home IP) - Communication protocol layer
- **SPINE** (Smart Premises Interoperable Network-Neutral) - Application protocol layer

## Architecture Layers

The architecture follows a layered approach from low-level networking to high-level use cases:

```
┌─────────────────────────────────────────┐
│           Use Cases Layer               │
│  (CEM, CS, EG, MA actors with           │
│   specific use case implementations)    │
├─────────────────────────────────────────┤
│           Features Layer                │
│  (Client/Server feature helpers for     │
│   common SPINE feature operations)      │
├─────────────────────────────────────────┤
│             Service Layer               │
│  (Central orchestration, device         │
│   management, event coordination)       │
├─────────────────────────────────────────┤
│             SPINE Layer                 │
│  (Application protocol, entities,       │
│   features, data models)                │
├─────────────────────────────────────────┤
│             SHIP Layer                  │
│  (Transport protocol, websockets,       │
│   mDNS, pairing, security)              │
└─────────────────────────────────────────┘
```

## Core Components

### 1. Service Layer (`service/`)

The central orchestration component that manages all aspects of the EEBUS service.

**Key Components:**

- `Service`: Main service implementation that coordinates all subsystems
- `ServiceInterface`: Defines the contract for service operations
- `ServiceReaderInterface`: Callback interface for service events

**Responsibilities:**

- Initialize and manage SHIP and SPINE layers
- Handle device connections and disconnections
- Coordinate use case implementations
- Manage mDNS service discovery
- Handle websocket connections
- Certificate and security management

**Key Methods:**

- `Setup()`: Initialize the service components
- `Start()`: Begin service operations
- `AddUseCase()`: Register use case implementations
- `RegisterRemoteSKI()`: Register a remote device for connection

### 2. Configuration (`api/configuration.go`)

Defines the service configuration parameters required for EEBUS operation.

**Key Parameters:**

- Device identification (brand, model, serial number, vendor code)
- Network configuration (port, interfaces, certificates)
- SPINE device and entity types
- mDNS service parameters
- Feature set definitions

### 3. Features Layer (`features/`)

Provides high-level abstractions for SPINE features with client/server role implementations.

#### Client Features (`features/client/`)

Features where the local entity acts as a client (consumer) of remote server features:

- `DeviceClassification`: Request device manufacturer information
- `DeviceConfiguration`: Read/write device configuration parameters
- `ElectricalConnection`: Access electrical connection parameters
- `LoadControl`: Interact with load control functionality
- `Measurement`: Request measurement data
- `TimeSeries`: Access time series data

#### Server Features (`features/server/`)

Features where the local entity acts as a server (provider) of feature functionality:

- `DeviceConfiguration`: Provide device configuration data
- `DeviceDiagnosis`: Report device state and diagnostics
- `LoadControl`: Accept and manage load control commands
- `Measurement`: Provide measurement data

#### Internal Features (`features/internal/`)

Common functionality shared between client and server implementations.

### 4. Use Cases Layer (`usecases/`)

Actor-based implementations of specific EEBUS use cases following the EEBUS specification.

#### Actors and Use Cases

**Customer Energy Management (CEM) — `usecases/cem/`:**

- `cevc`: Coordinated EV Charging
- `evcc`: EV Commissioning and Configuration
- `evcem`: EV Charging Electricity Measurement
- `evsecc`: EVSE Commissioning and Configuration
- `evsoc`: EV State Of Charge
- `opev`: Overload Protection by EV Charging Current Curtailment
- `oscev`: Optimization of Self-Consumption During EV Charging
- `vabd`: Visualization of Aggregated Battery Data
- `vapd`: Visualization of Aggregated Photovoltaic Data

**Controllable System (CS) — `usecases/cs/`:**

- `lpc`: Limitation of Power Consumption
- `lpp`: Limitation of Power Production

**Energy Guard (EG) — `usecases/eg/`:**

- `lpc`: Limitation of Power Consumption
- `lpp`: Limitation of Power Production

**Monitoring Appliance (MA) — `usecases/ma/`:**

- `mpc`: Monitoring of Power Consumption
- `mgcp`: Monitoring of Grid Connection Point

### 5. Use Case Base (`usecases/usecase/`)

Provides common functionality for all use case implementations:

**`UseCaseBase`:**

- Entity and actor type validation
- Scenario management
- Feature registration
- Event handling coordination
- Remote device compatibility checking

## Data Flow and Event Handling

### Connection Flow

1. **Service Initialization:**

   ```
   Configuration → Service.Setup() → SPINE Device Creation → mDNS Setup → SHIP Hub Setup
   ```

2. **Remote Device Discovery:**

   ```
   mDNS Discovery → Service Registry → Connection Attempt → SHIP Handshake → Device Registration
   ```

3. **Entity and Feature Discovery:**

   ```
   SPINE Discovery → Entity Registration → Feature Registration → Use Case Matching
   ```

### Event Processing Flow

1. **SHIP Layer Events:**
   - Connection/disconnection events
   - Pairing state updates
   - Service discovery updates

2. **SPINE Layer Events:**
   - Device/entity changes
   - Feature data updates
   - Binding and subscription changes

3. **Use Case Events:**
   - Use case specific data updates
   - State changes
   - Error conditions

### Message Flow

```
Application Layer (Use Cases)
         ↕
Feature Layer (Client/Server Helpers)
         ↕
Service Layer (Event Coordination)
         ↕
SPINE Layer (Application Protocol)
         ↕
SHIP Layer (Transport Protocol)
         ↕
Network Layer (WebSocket/TCP)
```

## Component Interactions

### Service Hub Interface

The service implements the `HubReaderInterface` to handle SHIP-level events:

- `RemoteSKIConnected()`: Handle successful remote connections
- `RemoteSKIDisconnected()`: Handle connection terminations
- `SetupRemoteDevice()`: Configure remote device communication
- `VisibleRemoteServicesUpdated()`: Process service discovery updates

### Use Case Integration

Use cases integrate with the service through:

1. **Feature Registration:** Use cases register required SPINE features
2. **Event Callbacks:** Use cases receive relevant SPINE events
3. **Data Access:** Use cases access remote device data through feature helpers
4. **State Management:** Use cases maintain scenario-specific state

### Feature Abstraction

Features provide abstraction over SPINE functionality:

- **Subscription Management:** Automatic subscription to remote feature updates
- **Data Requests:** Simplified methods for requesting remote data
- **Write Operations:** Safe write operations with proper validation
- **Event Filtering:** Automatic filtering of relevant events

## Security and Pairing

### Certificate Management

- X.509 certificate handling for device authentication
- Certificate validation and trust establishment
- Secure key exchange during pairing

### Pairing Process

1. **Discovery:** mDNS-based service discovery
2. **Initial Contact:** SHIP handshake initiation
3. **Authentication:** Certificate exchange and validation
4. **Trust Establishment:** User approval/automatic acceptance
5. **Secure Communication:** Encrypted message exchange

### Access Control

- SKI (Subject Key Identifier) based device identification
- Configurable auto-accept policies
- User interaction callbacks for pairing approval

## Configuration and Deployment

### Service Configuration

```go
configuration := api.NewConfiguration(
    vendorCode, brand, model, serial,
    deviceCategories, deviceType, entityTypes,
    port, certificate, heartbeatTimeout
)
```

### Use Case Registration

```go
service.AddUseCase(NewEVCC(service, localEntity, eventCallback))
```

### Event Handling

```go
func (h *Handler) HandleEvent(payload spineapi.EventPayload) {
    // Process use case specific events
}
```

## Extension Points

### Custom Use Cases

- Implement `UseCaseInterface` 
- Extend `UseCaseBase` for common functionality
- Register with service using `AddUseCase()`

### Custom Features

- Implement feature client/server interfaces
- Use internal feature helpers for common operations
- Register features with local entities

### Custom Event Handling

- Implement `EntityEventCallback` for use case events
- Implement `ServiceReaderInterface` for service events
- Process events through the established event flow

## Testing and Mocking

The architecture supports comprehensive testing through:

- Mock interfaces for all major components
- Test helpers for setting up device scenarios
- Integration test framework for end-to-end testing
- Feature-specific test suites

## Dependencies

### External Libraries

- `github.com/enbility/ship-go`: SHIP protocol implementation
- `github.com/enbility/spine-go`: SPINE protocol implementation

### Internal Structure

- Clear separation of concerns between layers
- Interface-based design for testability
- Event-driven architecture for loose coupling

This architecture provides a robust, extensible foundation for implementing EEBUS-based energy management solutions while maintaining clear separation between protocol layers and business logic.
