package protocol

// Version is the protocol version this build speaks. Agents auto-update
// independently, so the cloud must know which version each agent speaks.
// Bump MAJOR on a breaking wire change, MINOR on a compatible addition.
const Version = "1.0.0"

// MinSupportedAgentVersion is the oldest agent the cloud will serve.
// Agents below this are told to update before receiving jobs.
const MinSupportedAgentVersion = "1.0.0"
