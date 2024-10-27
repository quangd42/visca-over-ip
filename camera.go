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
	CommandPrefix      = "8101" // Default value for visca over ip
	CommandSuffix      = "FF"   // Message terminator
	PayloadTypeCommand = "0100" // Payload type for Command
	SequenceNumMax     = math.MaxUint32
)

type Config struct {
	MaxRetries int
	Timeout    time.Duration
	Debug      bool // TODO: Add logging for debugging
}

type Stats struct {
	missedResponses int
	timeouts        int
}

// Camera represents a peripheral device that can be controlled via VISCA over IP.
type Camera struct {
	Conn   *net.UDPConn
	seqNum int // Sequence Number
	config Config
	stats  Stats
}

// NewCamera returns a Camera struct that holds information to communicate
// with the peripheral device.
//
// Upon initialization, the struct will attempt to reset the sequence
// number and clear the interface socket of the connected peripheral device.
//
// MaxNumRetries can be updated post initialization.
func NewCamera(conn *net.UDPConn) (Camera, error) {
	cfg := Config{
		MaxRetries: 5,
		Timeout:    DefaultTimeout,
		Debug:      false,
	}
	return NewCameraWithConfig(conn, cfg)
}
func NewCameraWithConfig(conn *net.UDPConn, cfg Config) (Camera, error) {
	camera := Camera{
		Conn:   conn,
		seqNum: 0,
		config: cfg,
		stats:  Stats{},
	}
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

func (c *Camera) incSeqNum() int {
	c.seqNum += 1
	if c.seqNum > SequenceNumMax {
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

	payload := CommandPrefix + commandHex + CommandSuffix
	payloadLength := fmt.Sprintf("%04x", len(payload)/2)
	seqNumStr := fmt.Sprintf("%08x", seqNum)

	messageStr := PayloadTypeCommand + payloadLength + seqNumStr + payload
	message, err := hex.DecodeString(messageStr)
	if err != nil {
		return nil, errors.New("malformed command_hex")
	}

	return message, nil
}

func (c *Camera) SendCommand(commandHex string) error {
	seqNum := c.incSeqNum()
	message, err := MakeCommand(commandHex, seqNum)
	if err != nil {
		return err
	}

	for count := 1; ; count += 1 {
		if count > c.config.MaxRetries {
			return errors.New("peripheral device is not responsive")
		}

		err = c.Conn.SetDeadline(time.Now().Add(100 * time.Second))
		if err != nil {
			return fmt.Errorf("failed to set read deadline: %w", err)
		}
		_, err = c.Conn.Write(message)
		if err != nil {
			// If write times out, simply try again
			if errors.Is(err, os.ErrDeadlineExceeded) {
				c.config.Timeout += 1
				continue
			}
			return err
		}

		err := c.receiveCommandResponse(seqNum)
		if err != nil {
			// If read times out, simply consider response missed
			if errors.Is(err, os.ErrDeadlineExceeded) {
				c.config.Timeout += 1
				continue
			}
			return fmt.Errorf("response error: %w", err)
		}

		break
	}

	return nil
}

// receiveCommandResponse blocks until it times out or gets a response.
// If the response status code is not 4 (ACK) or 5 (completion) then it
// return the payload of the response as the error message.
func (c *Camera) receiveCommandResponse(seqNum int) error {
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
			err = c.Conn.SetDeadline(time.Now().Add(100 * time.Second))
			if err != nil {
				return err
			}
			continue
		case 5:
			return nil
		default:
			return fmt.Errorf("peripheral device error: %s", resPayload)
		}

	}
}

// ResetSequenceNumber calls RESET command to peripheral device, which
// resets its sequence number to 0. The value that was set as the
// sequence number is ignored.
func (c *Camera) ResetSequenceNumber() error {
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


func (c *Camera) Stats() string {
	return fmt.Sprintf(
		"Missed Responses: %d, Timeouts: %d",
		c.stats.missedResponses,
		c.stats.timeouts,
	)
}
