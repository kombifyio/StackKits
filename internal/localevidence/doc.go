// Package localevidence produces Architecture-v2 pre-Apply evidence under the
// local homelab owner's own signing custody.
//
// StackKits is an Open Source homelab standard that must operate with no
// Kombify account, no Kombify endpoint, and no TechStack. ADR-0029 states the
// same requirement from the trust side: for a homelab the Authority Site is
// home, "Enrollment and signing happen there", and conformance gates must
// reject remote enrollment/signing. A collector that can only be constructed
// by an authenticated remote service therefore cannot be the only collector.
//
// The applyevidence SPI was always local-capable: it carries only a
// CollectionRequest and canonical result bytes, and leaves "observation,
// enrollment, signing, endpoints, credentials, transport" private to the
// implementation. This package is the implementation that keeps all of that on
// the box, anchored to the owner established by `stackkit init
// --owner-source=local`.
//
// The collector never fabricates evidence. Each expectation is answered from
// facts actually gathered on this host, and any requirement kind that cannot be
// genuinely observed fails closed rather than being signed as satisfied. A
// rubber-stamp collector would be strictly worse than the refusal it replaces.
package localevidence
