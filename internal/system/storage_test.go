package system

import (
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLsblkOutput(t *testing.T) {
	jsonData := []byte(`{
   "blockdevices": [
      {"name":"sda", "path":"/dev/sda", "size":21474836480, "type":"disk", "mountpoint":null, "fstype":null, "model":"VBOX HARDDISK", "rm":false},
      {"name":"sdb", "path":"/dev/sdb", "size":34359738368, "type":"disk", "mountpoint":null, "fstype":null, "model":"USB Flash", "rm":true},
      {"name":"sdc", "path":"/dev/sdc", "size":1073741824, "type":"disk", "mountpoint":"/mnt/data", "fstype":"ext4", "model":"Other", "rm":false}
   ]
}`)

	devices, err := ParseLsblkOutput(jsonData)
	require.NoError(t, err)
	assert.Len(t, devices, 3)

	// sda: 20 GB disk
	assert.Equal(t, "sda", devices[0].Name)
	assert.Equal(t, "/dev/sda", devices[0].Path)
	assert.InDelta(t, 20.0, devices[0].SizeGB, 0.1)
	assert.Equal(t, "disk", devices[0].Type)
	assert.Equal(t, "", devices[0].Mountpoint)
	assert.Equal(t, "VBOX HARDDISK", devices[0].Model)
	assert.False(t, devices[0].Removable)

	// sdb: 32 GB removable
	assert.Equal(t, "sdb", devices[1].Name)
	assert.InDelta(t, 32.0, devices[1].SizeGB, 0.1)
	assert.True(t, devices[1].Removable)
	assert.Equal(t, "USB Flash", devices[1].Model)

	// sdc: 1 GB mounted
	assert.Equal(t, "/mnt/data", devices[2].Mountpoint)
	assert.Equal(t, "ext4", devices[2].FSType)
}

func TestParseLsblkOutput_Empty(t *testing.T) {
	jsonData := []byte(`{"blockdevices": []}`)
	devices, err := ParseLsblkOutput(jsonData)
	require.NoError(t, err)
	assert.Empty(t, devices)
}

func TestParseLsblkOutput_InvalidJSON(t *testing.T) {
	_, err := ParseLsblkOutput([]byte(`not json`))
	assert.Error(t, err)
}

func TestFilterAvailableDevices(t *testing.T) {
	devices := []models.BlockDevice{
		{Name: "sda", Path: "/dev/sda", SizeGB: 20, Type: "disk", Mountpoint: "/"},         // root — skip
		{Name: "sdb", Path: "/dev/sdb", SizeGB: 32, Type: "disk"},                          // available
		{Name: "sdc", Path: "/dev/sdc", SizeGB: 16, Type: "disk", Mountpoint: "/mnt/data"}, // mounted — skip
		{Name: "sdd", Path: "/dev/sdd", SizeGB: 8, Type: "disk", FSType: "ext4"},           // has filesystem — skip
		{Name: "sde", Path: "/dev/sde", SizeGB: 0.5, Type: "disk"},                         // too small — skip
		{Name: "sdf", Path: "/dev/sdf", SizeGB: 64, Type: "disk"},                          // available
		{Name: "sr0", Path: "/dev/sr0", SizeGB: 4, Type: "rom"},                            // not disk — skip
		{Name: "sda1", Path: "/dev/sda1", SizeGB: 20, Type: "part"},                        // partition — skip
	}

	available := FilterAvailableDevices(devices, "/dev/sda")

	assert.Len(t, available, 2)
	assert.Equal(t, "sdb", available[0].Name)
	assert.Equal(t, "sdf", available[1].Name)
}

func TestFilterAvailableDevices_NoAvailable(t *testing.T) {
	devices := []models.BlockDevice{
		{Name: "sda", Path: "/dev/sda", SizeGB: 20, Type: "disk", Mountpoint: "/"},
	}

	available := FilterAvailableDevices(devices, "/dev/sda")
	assert.Empty(t, available)
}
