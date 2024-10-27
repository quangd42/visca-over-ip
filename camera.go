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
	CommandPrefix        = "8101" // Default value for visca over ip
	CommandSuffix        = "FF"   // Message terminator
	PayloadTypeCommand   = "0100" // Payload type for Command
	SequenceNumMax       = math.MaxUint32
	StatusCodeACK        = 0x04
	StatusCodeCompletion = 0x05
	MessageBufferSize    = 24
	DefaultTimeout       = 100 * time.Millisecond
)

type Config struct {
	MaxRetries int
	Timeout    time.Duration
	Debug      bool
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
	cleaned := strings.ReplaceAll(commandHex, " ", "")

	if len(cleaned)%2 != 0 {
		return nil, fmt.Errorf("command hex must have even length: %s", commandHex)
	}

	payload := CommandPrefix + cleaned + CommandSuffix
	payloadLength := fmt.Sprintf("%04x", len(payload)/2)
	seqNumStr := fmt.Sprintf("%08x", seqNum)

	messageStr := PayloadTypeCommand + payloadLength + seqNumStr + payload
	message, err := hex.DecodeString(messageStr)
	if err != nil {
		return nil, fmt.Errorf("invalid hex in command: %s", commandHex)
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

		err = c.Conn.SetWriteDeadline(time.Now().Add(DefaultTimeout))
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
	res := make([]byte, MessageBufferSize)

	for {
		// NOTE: handle random request not from camera with ReadFrom

		// Set read deadline for timeout
		err := c.Conn.SetReadDeadline(time.Now().Add(DefaultTimeout))
		if err != nil {
			return fmt.Errorf("failed to set read deadline: %w", err)
		}
		bytesRead, err := c.Conn.Read(res)
		// Ensure message received has enough bytes for header (8)
		// and minimum payload (4)
		if bytesRead < 12 {
			return fmt.Errorf("response too short: got %d bytes, expected at least 12", bytesRead)
		}
		if err != nil {
			return err
		}

		resSeqNum := binary.BigEndian.Uint32(res[4:8])

		// Ignore late responses from earlier messages.
		// resSeqNum cannot be larger than seqNum.
		// When there are missed responses from peripheral device, the resSeqNum of subsequent
		// responses will be the same as seqNum, in which case we can continue processing.
		if int(resSeqNum) < seqNum {
			if c.config.Debug {
				fmt.Printf("Received old response: expected=%d, got=%d\n", seqNum, resSeqNum)
			}
			continue
		}

		// Extract payload (everything after first 8 bytes)
		resPayload := res[8:bytesRead]

		if len(resPayload) < 4 {
			return errors.New("response payload too short")
		}

		// Status code is at index 3 in the payload
		switch statusCode := resPayload[3]; statusCode {
		case StatusCodeACK:
			if c.config.Debug {
				fmt.Printf("Received ACK for sequence %d\n", seqNum)
			}
			continue
		case StatusCodeCompletion:
			if c.config.Debug {
				fmt.Printf("Received Completion for sequence %d\n", seqNum)
			}
			return nil
		default:
			return fmt.Errorf(
				"peripheral device error: payload=%x, statusCode=%x",
				resPayload, statusCode,
			)
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
