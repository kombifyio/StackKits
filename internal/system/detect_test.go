package system

import "testing"

func TestParseVirtType(t *testing.T) {
	tests := []struct {
		input string
		want  VirtType
	}{
		{"kvm", VirtKVM},
		{"qemu", VirtKVM},
		{"vmware", VirtVMware},
		{"microsoft", VirtHyperV},
		{"xen", VirtXen},
		{"openvz", VirtOpenVZ},
		{"lxc", VirtLXC},
		{"lxc-libvirt", VirtLXC},
		{"docker", VirtDocker},
		{"wsl", VirtWSL},
		{"none", VirtNone},
		{"something-else", VirtUnknown},
		{"KVM", VirtKVM},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := parseVirtType(tt.input); got != tt.want {
				t.Fatalf("parseVirtType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsContainerBlocked(t *testing.T) {
	for _, virt := range []VirtType{VirtOpenVZ, VirtLXC} {
		if !IsContainerBlocked(virt) {
			t.Fatalf("%s should block containers", virt)
		}
	}
	for _, virt := range []VirtType{VirtNone, VirtKVM, VirtDocker, VirtUnknown} {
		if IsContainerBlocked(virt) {
			t.Fatalf("%s should not be classified as blocking containers", virt)
		}
	}
}
