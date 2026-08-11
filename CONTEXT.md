# Context — Layr8 Go SDK

The Go SDK for building actors on the Layr8 platform. Actors connect to cloud-nodes via WebSocket and exchange DIDComm v2 messages with other actors across the network.

## Language

### Core

**Actor**:
A software process that connects to a cloud-node and exchanges DIDComm v2 messages. An actor is identified by a DID. Actors can be agents, humans, workflows, services, gateways, or any other type.
_Avoid_: agent (too narrow — agent is one species of actor), client (ambiguous with the SDK object), service

**Cloud-node**:
A Layr8 infrastructure component that routes DIDComm messages between actors. Actors connect via WebSocket using the Phoenix Channel V2 protocol.
_Avoid_: server, node (ambiguous), hub

**Plugin**:
A live channel session between an actor and a cloud-node. Created on join, destroyed on disconnect. One actor can have multiple plugins — either different protocol bindings on the same cloud-node (like binding different ports on the same IP) or connections to different cloud-nodes.
_Avoid_: connection, socket, session

**DID**:
Decentralized Identifier — the globally unique identity of an actor (e.g., `did:web:myorg:my-agent`). May be configured explicitly or assigned by the cloud-node (ephemeral).
_Avoid_: address, identity, key

**Protocol**:
A DIDComm protocol identified by a URI (e.g., `https://layr8.io/protocols/echo/1.0`). Defines a set of related message types. The SDK derives protocol URIs automatically from registered handler message types.
_Avoid_: PIURI (too spec-jargony for SDK users), payload_type (internal wire term)

**Message Type**:
A specific message within a protocol, identified by appending a slug to the protocol URI (e.g., `https://layr8.io/protocols/echo/1.0/request`).
_Avoid_: event, action

**Handler**:
A function registered for a specific message type. Receives a `*Message`, returns a response `*Message`, `nil`, `ErrPass`, or an error.
_Avoid_: callback, listener, subscriber

**PASS**:
A sentinel error (`ErrPass`) returned by a handler to decline a message. Signals to the cloud-node that this actor does not handle this message type, so it should try the next matching handler.
_Avoid_: skip, reject, decline

### Reply Protocol

**Reply Protocol**:
A capability-negotiated dispatch protocol between the SDK and cloud-node. When supported (`reply_protocol/1` in join capabilities), the SDK sends a `dispatch_reply` event after each dispatched message instead of legacy acks.

**Dispatch Reply**:
An event sent to the cloud-node after handling a dispatched message. Status is `handled`, `pass`, or `error`. Replaces legacy ack in reply-protocol mode.
_Avoid_: ack, acknowledgment (legacy mode only)

**Capability Negotiation**:
On join, the SDK sends `reply_protocol: true`. The cloud-node responds with a `capabilities` list (e.g., `["reply_protocol/1", "wildcard/1"]`). The SDK adapts its dispatch behavior based on the server's capabilities.

### Compat Suite

**Scenario**:
A cross-language compatibility test case. Each scenario is a pair of functions (`runReceiver`, `runSender`) that exercise a specific SDK behavior against a cloud-node.

**Compat Image**:
A Docker image (`ghcr.io/layr8/go-sdk/compat:{version}`) that packages scenario code and a CLI adapter. Consumed by the compatibility orchestrator.

**Ready Signal**:
A JSON line (`{"status":"ready","did":"..."}`) printed to stdout by a receiver process after connecting and registering handlers. The orchestrator waits for this before launching the sender.

**Layer 1**:
Go test adapter — runs scenarios against real cloud-node Docker containers in-process.

**Layer 2**:
CLI adapter — implements the compatibility orchestrator's interface (`--mode`, `--scenario`, `--node`, `--did`, `--list-scenarios`).

**Compatibility orchestrator**:
A separate repository that pairs SDK compat images across languages and cloud-node versions, runs test matrices, and produces compatibility reports.

## Relationships

- An **Actor** is identified by exactly one **DID**
- An **Actor** can have multiple **Plugins** (different protocol bindings or different cloud-nodes)
- A **Plugin** is uniquely identified by the combination of **DID** + bound **Protocols** on a **Cloud-node**
- A **Protocol** contains one or more **Message Types**
- A **Handler** is registered for one **Message Type** (or all, via catch-all)
- A **Scenario** has exactly one `runReceiver` and one `runSender`
- A **Compat Image** packages all **Scenarios** plus the **Layer 2** CLI adapter
- **Layer 1** and **Layer 2** are both adapters over the same **Scenario** functions

## Example dialogue

> **Dev:** "Can I connect two plugins with the same DID to the same cloud-node?"
> **Domain expert:** "Yes — as long as they bind different protocols. Think of it like binding 127.0.0.1:80 and 127.0.0.1:90. Same address, different ports, no conflict."

> **Dev:** "When a handler returns ErrPass, what happens?"
> **Domain expert:** "The SDK sends a dispatch_reply with status 'pass' to the cloud-node. The cloud-node then tries the next plugin that registered for that protocol."

> **Dev:** "What if my actor only sends messages and never receives?"
> **Domain expert:** "Use Config.Protocols to declare which protocols you participate in. The SDK merges those with handler-derived protocols and always includes the problem-report protocol."

## Flagged ambiguities

- **"agent"** was used broadly in early docs to mean any connected process. Resolved: **Actor** is the general term. Agent is one type of actor.
- **"plugin"** appears in the WebSocket path (`/plugin_socket/websocket`) and channel topic (`plugins:{did}`). This is the cloud-node's term for the channel session. SDK users should think in terms of **Actor** (their code) and **Plugin** (the live session).
- **"payload_types"** is the wire-format join parameter that carries protocol URIs. SDK users register handlers by **Message Type**; the SDK derives **Protocols** automatically.
- **"ack"** is legacy terminology from before the reply protocol. In reply-protocol mode, the SDK sends **Dispatch Replies** instead. Legacy ack is still supported for older cloud-nodes.
