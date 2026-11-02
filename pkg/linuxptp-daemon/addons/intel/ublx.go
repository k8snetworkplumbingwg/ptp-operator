package intel

import (
	"github.com/k8snetworkplumbingwg/linuxptp-daemon/pkg/ublox"
)

// UblxCmdList is a list of UblxCmd items.
// This is a type alias for ublox.CommandList, preserving JSON compatibility
// with existing plugin configurations.
type UblxCmdList = ublox.CommandList

// UblxCmd represents a single ublox command.
// This is a type alias for ublox.Command, preserving JSON compatibility
// with existing plugin configurations.
type UblxCmd = ublox.Command
