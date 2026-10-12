---
name: smart-home-mqtt-discovery
description: Operate an explicitly selected native Mosquitto broker and bind the admitted Home Assistant and Zigbee2MQTT integration through upstream interfaces.
---

# Smart Home MQTT discovery

Verify the current Plan selects smart-home-mqtt=mosquitto before broker operations. Home Assistant, MQTT and Zigbee2MQTT are separate optional workloads. Use the existing native lifecycle MCP/CLI and validate/generate the saved choices. Never install a second broker or create a Kombify device bridge.

The broker consumes its mqtt-password slot from encrypted node custody through the existing private secret-file delivery. Preserve that reference during retry. Client credentials belong in a private upstream client configuration, not arguments, tool outputs or generated artifacts. Verify authenticated publish/subscribe and anonymous denial; an HTTP systree response does not prove MQTT delivery. Retained application messages and the broker persistence file must survive a graceful restart.

The MQTT listener is private to its module network unless the Plan explicitly declares lan-listener. Do not infer LAN access from a dashboard route or enable TCP1883 publicly. HTTPS dashboard ingress does not encrypt MQTT transport. A different Compose project cannot use an assumed mosquitto service hostname: use the actually declared reachable broker address and its admitted networking/custody.

For Home Assistant, use the authenticated upstream MQTT integration/config-flow on its admitted instance. Bind the actual broker address, port and node-custodied credentials. Preserve its existing discovery prefix, integration and entity exposure choices. Observe both discovery of a controlled entity and a subsequent actual state change through Home Assistant's API/MCP; a published discovery payload alone is not that evidence. Missing managed recorder/database assignment blocks the dependent application flow. Do not start a local or emulated database or silently disable persistence to bypass admission.

For smart-home-zigbee=zigbee2mqtt, require the owner-selected real usb-device, mqtt-server and adapter intent before apply. Reference the same broker credential in the saved Plan when both workloads use that broker; never copy a password into configuration.yaml. The existing native executor delivers a file reference because upstream ZIGBEE2MQTT_CONFIG variables otherwise persist into its data volume. Home Assistant discovery is enabled in this pinned bundle, but radio/device claims still require actual hardware evidence. Do not fabricate a serial device or broker.

Keep upstream setup, automation, playback and device behavior upstream-owned. No provider resource, another node's lifecycle or implicit external access belongs to this skill. Report the proven broker boundary separately from Core/private HTTPS, HA setup/discovery/state, Zigbee hardware, household, current-public-Latest and backup/restore/remove acceptance.
