// Package smarthome - Smart Home Add-On
//
// Home automation stack:
//   - Home Assistant: Smart home hub
//   - Mosquitto: MQTT broker for IoT devices
//   - Zigbee2MQTT: Zigbee device bridge (if Zigbee coordinator present)
//
// License:
//   - Home Assistant: Apache-2.0
//   - Mosquitto: EPL-2.0 / EDL-1.0
//   - Zigbee2MQTT: GPL-3.0
//
// Placement: Local node (needs hardware access for Zigbee/Z-Wave)
//
// Usage:
//   addons: "smart-home": smarthome.#Config & {
//       zigbee2mqtt: enabled: true
//       zigbee2mqtt: device: "/dev/ttyUSB0"
//   }

package smarthome

// #Config defines smart home add-on configuration
#Config: {
	_addon: {
		name:        "smart-home"
		displayName: "Smart Home"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Home Assistant + MQTT + Zigbee2MQTT"
	}

	enabled: bool | *true

	// Home Assistant configuration
	homeassistant: #HomeAssistantConfig

	// MQTT broker
	mosquitto: #MosquittoConfig

	// Zigbee bridge
	zigbee2mqtt: #Zigbee2MQTTConfig
}

#HomeAssistantConfig: {
	// Network mode (host mode recommended for mDNS discovery)
	networkMode: *"host" | "bridge"

	// Bluetooth support
	bluetooth: bool | *false

	// Resource limits
	resources: {
		memory: string | *"1024m"
		cpus:   number | *2.0
	}
}

#MosquittoConfig: {
	enabled: bool | *true

	// Authentication
	auth: {
		enabled:  bool | *true
		username: string | *"mqtt"
		password: =~"^secret://"
	}

	// Persistence
	persistence: bool | *true

	// Resource limits
	resources: {
		memory: string | *"64m"
		cpus:   number | *0.25
	}
}

#Zigbee2MQTTConfig: {
	enabled: bool | *false

	// Zigbee coordinator device path
	device: string | *"/dev/ttyUSB0"

	// Coordinator type
	adapter: *"zstack" | "deconz" | "ember" | "zboss"

	// Permit new device joining
	permitJoin: bool | *false

	// Frontend web UI
	frontend: bool | *true

	// Resource limits
	resources: {
		memory: string | *"256m"
		cpus:   number | *0.5
	}
}

// Service definitions

#HomeAssistantService: {
	name:        "home-assistant"
	displayName: "Home Assistant"
	image:       "ghcr.io/home-assistant/home-assistant:2025.2"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 8123, host: 8123, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "hass-config", path: "/config", type: "volume"},
		{host: "/etc/localtime", path: "/etc/localtime", type: "bind", readOnly: true},
	]

	traefik: {
		enabled: true
		rule:    "Host(`home.{{.domain}}`)"
	}
}

#MosquittoService: {
	name:        "mosquitto"
	displayName: "Mosquitto"
	image:       "eclipse-mosquitto:2"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 1883, host: 1883, protocol: "tcp", name: "mqtt"},
		{container: 9001, host: 9001, protocol: "tcp", name: "websockets"},
	]

	volumes: [
		{name: "mosquitto-data", path: "/mosquitto/data", type: "volume"},
		{name: "mosquitto-config", path: "/mosquitto/config", type: "volume"},
		{name: "mosquitto-log", path: "/mosquitto/log", type: "volume"},
	]
}

#Zigbee2MQTTService: {
	name:        "zigbee2mqtt"
	displayName: "Zigbee2MQTT"
	image:       "koenkk/zigbee2mqtt:1"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 8080, host: 8086, protocol: "tcp", name: "frontend"},
	]

	volumes: [
		{name: "z2m-data", path: "/app/data", type: "volume"},
	]

	// Device access for Zigbee coordinator
	devices: ["/dev/ttyUSB0:/dev/ttyUSB0"]

	traefik: {
		enabled: true
		rule:    "Host(`zigbee.{{.domain}}`)"
	}
}

#Outputs: {
	hassUrl:      string | *"https://home.{{.domain}}"
	mqttBroker:   string | *"mqtt://mosquitto:1883"
	zigbeeUrl?:   string
}
