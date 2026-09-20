// Package blynk implements the Blynk binary wire protocol as spoken by Plaato
// Keg hardware.
//
// Command constants are taken from
// https://github.com/blynkkk/blynk-library/blob/7e942d661bc54ded310bf5d00edee737d0ca44d7/src/Blynk/BlynkProtocolDefs.h
//
// Frame layout, all integers big-endian:
//
//	[cmd uint8][msgID uint16][length uint16][body length bytes]
//
// A response frame (cmd 0) is the exception: its third field carries a status
// code rather than a body length, and the frame ends there — 5 bytes total.
package blynk

import "encoding/binary"

// Command is the first byte of a Blynk frame.
type Command uint8

const (
	CmdResponse          Command = 0
	CmdRegister          Command = 1
	CmdLogin             Command = 2
	CmdSaveProf          Command = 3
	CmdLoadProf          Command = 4
	CmdGetToken          Command = 5
	CmdPing              Command = 6
	CmdActivate          Command = 7
	CmdDeactivate        Command = 8
	CmdRefresh           Command = 9
	CmdGetGraphData      Command = 10
	CmdGetGraphDataResp  Command = 11
	CmdTweet             Command = 12
	CmdEmail             Command = 13
	CmdNotify            Command = 14
	CmdBridge            Command = 15
	CmdHardwareSync      Command = 16
	CmdInternal          Command = 17
	CmdSMS               Command = 18
	CmdProperty          Command = 19
	CmdHardware          Command = 20
	CmdCreateDash        Command = 21
	CmdSaveDash          Command = 22
	CmdDeleteDash        Command = 23
	CmdLoadProfGz        Command = 24
	CmdSync              Command = 25
	CmdSharing           Command = 26
	CmdAddPushToken      Command = 27
	CmdGetSharedDash     Command = 29
	CmdGetShareToken     Command = 30
	CmdRefreshShareToken Command = 31
	CmdShareLogin        Command = 32
	CmdRedirect          Command = 41
	CmdDebugPrint        Command = 55
	CmdEventLog          Command = 64
)

var commandNames = map[Command]string{
	CmdResponse: "response", CmdRegister: "register", CmdLogin: "login",
	CmdSaveProf: "save_prof", CmdLoadProf: "load_prof", CmdGetToken: "get_token",
	CmdPing: "ping", CmdActivate: "activate", CmdDeactivate: "deactivate",
	CmdRefresh: "refresh", CmdGetGraphData: "get_graph_data",
	CmdGetGraphDataResp: "get_graph_data_response", CmdTweet: "tweet",
	CmdEmail: "email", CmdNotify: "notify", CmdBridge: "bridge",
	CmdHardwareSync: "hardware_sync", CmdInternal: "internal", CmdSMS: "sms",
	CmdProperty: "property", CmdHardware: "hardware", CmdCreateDash: "create_dash",
	CmdSaveDash: "save_dash", CmdDeleteDash: "delete_dash", CmdLoadProfGz: "load_prof_gz",
	CmdSync: "sync", CmdSharing: "sharing", CmdAddPushToken: "add_push_token",
	CmdGetSharedDash: "get_shared_dash", CmdGetShareToken: "get_share_token",
	CmdRefreshShareToken: "refresh_share_token", CmdShareLogin: "share_login",
	CmdRedirect: "redirect", CmdDebugPrint: "debug_print", CmdEventLog: "event_log",
}

// String returns the Blynk name for the command, or "unknown_cmd" for the
// opcodes the protocol leaves unassigned (28, 33-40, 42-54, 56-63).
func (c Command) String() string {
	if name, ok := commandNames[c]; ok {
		return name
	}
	return "unknown_cmd"
}

// Status is the code carried in a response frame.
type Status uint16

const (
	StatusSuccess               Status = 200
	StatusQuotaLimitException   Status = 1
	StatusIllegalCommand        Status = 2
	StatusNotRegistered         Status = 3
	StatusAlreadyRegistered     Status = 4
	StatusNotAuthenticated      Status = 5
	StatusNotAllowed            Status = 6
	StatusDeviceNotInNetwork    Status = 7
	StatusNoActiveDashboard     Status = 8
	StatusInvalidToken          Status = 9
	StatusIllegalCommandBody    Status = 11
	StatusGetGraphDataException Status = 12
	StatusNtfInvalidBody        Status = 13
	StatusNtfNotAuthorized      Status = 14
	StatusNtfException          Status = 15
	StatusTimeout               Status = 16
	StatusNoDataException       Status = 17
	StatusDeviceWentOffline     Status = 18
	StatusServerException       Status = 19
	StatusNotSupportedVersion   Status = 20
	StatusEnergyLimit           Status = 21
)

// HeaderSize is the length of a Blynk frame header in bytes.
const HeaderSize = 5

// Frame is a single decoded Blynk message.
type Frame struct {
	Cmd   Command
	MsgID uint16
	// Body is nil for response frames, which carry a Status instead.
	Body []byte
	// Status is only meaningful when Cmd is CmdResponse.
	Status Status
}

// Encode serialises the frame in the standard 5-byte-header form.
func (f Frame) Encode() []byte {
	out := make([]byte, HeaderSize+len(f.Body))
	out[0] = byte(f.Cmd)
	binary.BigEndian.PutUint16(out[1:3], f.MsgID)
	binary.BigEndian.PutUint16(out[3:5], uint16(len(f.Body)))
	copy(out[HeaderSize:], f.Body)
	return out
}

// ResponseSuccess builds the 5-byte acknowledgement the Plaato firmware expects:
// 00 <msgID hi> <msgID lo> 00 C8.
//
// The msgID must echo the inbound frame's: older firmware validates it before
// firing BLYNK_CONNECTED, and a hardcoded id means the login ack is never
// recognised when internal metadata was sent first.
func ResponseSuccess(msgID uint16) []byte {
	out := make([]byte, HeaderSize)
	out[0] = byte(CmdResponse)
	binary.BigEndian.PutUint16(out[1:3], msgID)
	binary.BigEndian.PutUint16(out[3:5], uint16(StatusSuccess))
	return out
}

// Command builds an outbound frame with the given body.
func NewCommand(cmd Command, msgID uint16, body []byte) []byte {
	return Frame{Cmd: cmd, MsgID: msgID, Body: body}.Encode()
}
