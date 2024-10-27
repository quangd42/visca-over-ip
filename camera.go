package viscaoverip

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"time"
)

const (
	COMMAND_PREFIX      = "8101" // Default value for visca over ip
	COMMAND_SUFFIX      = "FF"   // Message terminator
	PAYLOADTYPE_COMMAND = "0100" // Payload type for Command
	SEQUENCE_NUM_MAX    = math.MaxUint32
)

type Camera struct {
	Conn                 *net.UDPConn
	seqNum               int // Sequence Number
	missedResponsesCount int
	MaxNumRetries        int
}

// NewCamera returns a Camera struct that holds information to communicate
// with the peripheral device.
//
// Upon initialization, the struct will attempt to reset the sequence
// number and clear the interface socket of the connected peripheral device.
//
// MaxNumRetries can be updated post initialization.
func NewCamera(conn *net.UDPConn) (Camera, error) {
	camera := Camera{conn, 0, 0, 5}
	err := camera.ResetSequenceNumber()
	if err != nil {
		return Camera{}, err
	}
	err = camera.SendCommand("00 01") // NOTE: clear the camera's interface socket, but why?
	if err != nil {
		return Camera{}, err
	}
	return camera, nil
}

func (c Camera) incSeqNum() int {
	c.seqNum += 1
	if c.seqNum > SEQUENCE_NUM_MAX {
		c.seqNum = 0
	}
	return c.seqNum
}

// MakeCommand is a convenience function that takes the hex string
// representation of command payload and returns the binary message
// to communicate to peripheral device.
func MakeCommand(commandHex string, seqNum int) ([]byte, error) {
	// Allow input string to contain spaces for legibility
	commandHex = strings.ReplaceAll(commandHex, " ", "")

	payload := COMMAND_PREFIX + commandHex + COMMAND_SUFFIX
	payloadLength := fmt.Sprintf("%04x", len(payload)/2)
	seqNumStr := fmt.Sprintf("%08x", seqNum)

	messageStr := PAYLOADTYPE_COMMAND + payloadLength + seqNumStr + payload
	message, err := hex.DecodeString(messageStr)
	if err != nil {
		return nil, errors.New("malformed command_hex")
	}

	return message, nil
}

func (c Camera) SendCommand(commandHex string) error {
	seqNum := c.incSeqNum()
	message, err := MakeCommand(commandHex, seqNum)
	if err != nil {
		return err
	}

	for count := 1; ; count += 1 {
		if count > c.MaxNumRetries {
			return errors.New("peripheral device is not responsive")
		}

		c.Conn.SetDeadline(time.Now().Add(100 * time.Second))
		_, err = c.Conn.Write(message)
		if err != nil {
			// If write times out, simply try again
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			return err
		}

		err := c.receiveCommandResponse(seqNum)
		if err != nil {
			// If read times out, simply consider response missed
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			return fmt.Errorf("response error: %s\n", err)
		}

		break
	}

	return nil
}

// receiveCommandResponse blocks until it times out or gets a response.
// If the response status code is not 4 (ACK) or 5 (completion) then it
// return the payload of the response as the error message.
func (c Camera) receiveCommandResponse(seqNum int) error {
	var res []byte

	for {
		// NOTE: handle random request not from camera with ReadFrom
		bytesReadCount, err := c.Conn.Read(res)
		if bytesReadCount == 0 {
			return errors.New("empty response received")
		}
		if err != nil {
			return err
		}

		var resSeqNum int
		_, err = binary.Decode(res[4:8], binary.BigEndian, resSeqNum)
		if err != nil {
			return err
		}
		// ignore late responses from earlier messages
		if resSeqNum < seqNum {
			continue
		}

		var resPayload []byte
		_, err = binary.Decode(res[8:], binary.BigEndian, resPayload)
		if err != nil {
			return err
		}
		// TODO: test if this is correct way of getting statusCode
		switch statusCode := int(resPayload[3:4][0]); statusCode {
		case 4:
			c.Conn.SetDeadline(time.Now().Add(100 * time.Second))
			continue
		case 5:
			return nil
		default:
			return fmt.Errorf("peripheral device error: %s \n", resPayload)
		}

	}
}

// ResetSequenceNumber calls RESET command to peripheral device, which
// resets its sequence number to 0. The value that was set as the
// sequence number is ignored.
func (c Camera) ResetSequenceNumber() error {
	_, err := c.Conn.Write([]byte("02 00 00 01 00 00 00 01 01"))
	if err != nil {
		return err
	}
	var res, resPayload []byte
	bytesRead, err := c.Conn.Read(res)
	if bytesRead == 0 || err != nil {
		return errors.New("failed to reset sequence numeber")
	}
	_, err = binary.Decode(res[8:], binary.BigEndian, resPayload)
	if err != nil || int(resPayload[0]) != 0x01 {
		return errors.New("failed to reset sequence numeber")
	}
	c.seqNum = 1
	return nil
}
