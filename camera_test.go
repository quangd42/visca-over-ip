package viscaoverip_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	voip "github.com/quangd42/visca-over-ip"
)

func TestMakeCommand(t *testing.T) {
	type testCase struct {
		name    string
		command string
		seqNum  int
		wantStr string
	}
	tests := []testCase{
		{
			"Pantilt: Home",
			"06 04",
			1234, // 04D2
			"0100 0005 000004D2 8101 06 04 FF",
		},
		{
			"Pantilt: Up Max Speed",
			"06 01 18 14 03 01",
			1865, // 0749
			"0100 0009 00000749 81 01 06 01 18 14 03 01 FF",
		},
		{
			"Cam Zoom: Tele (Variable)",
			"04 07 22", // 81 01 04 07 2p FF: p=0(low)~7(high)
			2489321654, // 946008B6
			"0100 0006 946008B6 81 01 04 07 22 FF",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantStr := strings.ReplaceAll(tc.wantStr, " ", "")
			want, err := hex.DecodeString(wantStr)
			if err != nil {
				t.Fatal(err)
			}

			message, err := voip.MakeCommand(tc.command, tc.seqNum)
			if !bytes.Equal(message, want) || err != nil {
				t.Errorf("\n%s,\nMakeCommand(%s) = %#v, %v,\nwant %#v, nil", tc.name, tc.command, message, err, want)
			}
		})
	}
}

type mockServer struct {
	conn    *net.UDPConn
	handler func([]byte) [][]byte
	done    chan struct{}
	wg      sync.WaitGroup
}

func newMockServer(t *testing.T) (*mockServer, string) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}

	server := &mockServer{
		conn: conn,
		done: make(chan struct{}),
	}

	server.wg.Add(1)
	go server.serve()

	return server, conn.LocalAddr().String()
}

func (s *mockServer) serve() {
	defer s.wg.Done()

	buf := make([]byte, 1024)
	for {
		select {
		case <-s.done:
			return
		default:
			s.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, remoteAddr, err := s.conn.ReadFrom(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				return
			}

			if s.handler != nil {
				responses := s.handler(buf[:n])
				for _, response := range responses {
					time.Sleep(1 * time.Millisecond) // Small delay between responses
					_, err = s.conn.WriteTo(response, remoteAddr)
					if err != nil {
						return
					}
				}
			}
		}
	}
}

func (s *mockServer) close() {
	close(s.done)
	s.conn.Close()
	s.wg.Wait()
}

// Helper function to create response messages
func makeResponse(seqNum uint32, statusCode byte) []byte {
	response := make([]byte, 12)
	binary.BigEndian.PutUint16(response[0:2], 0x0101) // Response type
	binary.BigEndian.PutUint16(response[2:4], 0x0004) // Payload length
	binary.BigEndian.PutUint32(response[4:8], seqNum) // Sequence number
	response[8] = 0x90                                // Response prefix
	response[9] = statusCode                          // Status code (0x4z or 0x5z)
	response[10] = 0x01                               // Socket number
	response[11] = 0xFF                               // Terminator
	return response
}

// Helper function to create reset response
func makeResetResponse() []byte {
	response := make([]byte, 9)
	binary.BigEndian.PutUint16(response[0:2], 0x0111)     // Reset response type
	binary.BigEndian.PutUint16(response[2:4], 0x0001)     // Payload length
	binary.BigEndian.PutUint32(response[4:8], 0x00000001) // Sequence number
	response[8] = 0x01                                    // Reset acknowledge
	return response
}

func TestSendCommand(t *testing.T) {
	tests := []struct {
		name           string
		setupHandler   func(*mockServer)
		expectedStats  string
		expectedError  bool
		expectedErrMsg string
	}{
		{
			name: "Success - ACK and Completion",
			setupHandler: func(s *mockServer) {
				initialized := false
				s.handler = func(msg []byte) [][]byte {
					// Check if this is a reset command (first two bytes are 0x0200)
					if !initialized && len(msg) >= 2 && msg[0] == 0x02 && msg[1] == 0x00 {
						initialized = true
						return [][]byte{makeResetResponse()}
					}
					// Check if this is the interface clear command
					if !initialized && bytes.Contains(msg, []byte{0x81, 0x01, 0x00, 0x01, 0xFF}) {
						return [][]byte{
							makeResponse(1, 0x41), // ACK
							makeResponse(1, 0x51), // Completion
						}
					}

					seqNum := binary.BigEndian.Uint32(msg[4:8])
					return [][]byte{
						makeResponse(seqNum, 0x41), // ACK
						makeResponse(seqNum, 0x51), // Completion
					}
				}
			},
			expectedStats: "Missed Responses: 0, Timeouts: 0",
			expectedError: false,
		},
		{
			name: "Lost Completion - Retry Success",
			setupHandler: func(s *mockServer) {
				initialized := false
				firstCommand := true
				s.handler = func(msg []byte) [][]byte {
					// Handle initialization sequence
					if !initialized && len(msg) >= 2 && msg[0] == 0x02 && msg[1] == 0x00 {
						initialized = true
						return [][]byte{makeResetResponse()}
					}
					if !initialized && bytes.Contains(msg, []byte{0x81, 0x01, 0x00, 0x01, 0xFF}) {
						return [][]byte{
							makeResponse(1, 0x41), // ACK
							makeResponse(1, 0x51), // Completion
						}
					}

					seqNum := binary.BigEndian.Uint32(msg[4:8])
					if firstCommand {
						firstCommand = false
						return [][]byte{makeResponse(seqNum, 0x41)} // Only ACK
					}
					return [][]byte{
						makeResponse(seqNum, 0x41), // ACK
						makeResponse(seqNum, 0x51), // Completion
					}
				}
			},
			expectedStats: "Missed Responses: 1, Timeouts: 0",
			expectedError: false,
		},
		{
			name: "Lost First Message - Second Attempt Success",
			setupHandler: func(s *mockServer) {
				initialized := false
				firstCommand := true
				s.handler = func(msg []byte) [][]byte {
					// Handle initialization sequence
					if !initialized && len(msg) >= 2 && msg[0] == 0x02 && msg[1] == 0x00 {
						initialized = true
						return [][]byte{makeResetResponse()}
					}
					if !initialized && bytes.Contains(msg, []byte{0x81, 0x01, 0x00, 0x01, 0xFF}) {
						return [][]byte{
							makeResponse(1, 0x41), // ACK
							makeResponse(1, 0x51), // Completion
						}
					}

					if firstCommand {
						firstCommand = false
						return nil // Ignore first message
					}
					seqNum := binary.BigEndian.Uint32(msg[4:8])
					return [][]byte{
						makeResponse(seqNum, 0x41), // ACK
						makeResponse(seqNum, 0x51), // Completion
					}
				}
			},
			expectedStats: "Missed Responses: 1, Timeouts: 0",
			expectedError: false,
		},
		{
			name: "Camera Returns Error Response",
			setupHandler: func(s *mockServer) {
				initialized := false
				s.handler = func(msg []byte) [][]byte {
					// Handle initialization sequence
					if !initialized && len(msg) >= 2 && msg[0] == 0x02 && msg[1] == 0x00 {
						initialized = true
						return [][]byte{makeResetResponse()}
					}

					seqNum := binary.BigEndian.Uint32(msg[4:8])
					if bytes.Contains(msg, []byte{0x81, 0x01, 0x00, 0x01, 0xFF}) {
						initialized = true
						return [][]byte{
							makeResponse(seqNum, 0x41), // ACK
							makeResponse(seqNum, 0x51), // Completion
						}
					}

					return [][]byte{
						makeResponse(seqNum, 0x41), // ACK
						makeResponse(seqNum, 0x60), // Error response (syntax error)
					}
				}
			},
			expectedStats:  "Missed Responses: 0, Timeouts: 0",
			expectedError:  true,
			expectedErrMsg: "response error: peripheral device error: payload=906001ff, statusCode=6",
		},
		{
			name: "Camera Returns Command Buffer Full Error",
			setupHandler: func(s *mockServer) {
				initialized := false
				s.handler = func(msg []byte) [][]byte {
					// Handle initialization sequence
					if !initialized && len(msg) >= 2 && msg[0] == 0x02 && msg[1] == 0x00 {
						initialized = true
						return [][]byte{makeResetResponse()}
					}

					seqNum := binary.BigEndian.Uint32(msg[4:8])
					if bytes.Contains(msg, []byte{0x81, 0x01, 0x00, 0x01, 0xFF}) {
						initialized = true
						return [][]byte{
							makeResponse(seqNum, 0x41), // ACK
							makeResponse(seqNum, 0x51), // Completion
						}
					}

					return [][]byte{
						makeResponse(seqNum, 0x41), // ACK
						makeResponse(seqNum, 0x61), // Error response (command buffer full)
					}
				}
			},
			expectedStats:  "Missed Responses: 0, Timeouts: 0",
			expectedError:  true,
			expectedErrMsg: "response error: peripheral device error: payload=906101ff, statusCode=6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, addr := newMockServer(t)
			defer server.close()

			tt.setupHandler(server)

			udpAddr, err := net.ResolveUDPAddr("udp", addr)
			if err != nil {
				t.Fatal(err)
			}

			conn, err := net.DialUDP("udp", nil, udpAddr)
			if err != nil {
				t.Fatal(err)
			}

			cfg := voip.Config{
				MaxRetries: 3,
				Timeout:    50 * time.Millisecond,
				Debug:      true,
			}

			camera, err := voip.NewCameraWithConfig(conn, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer camera.Close()

			err = camera.SendCommand("06 04")
			if (err != nil) != tt.expectedError {
				t.Errorf("SendCommand() error = %v, expectedError = %v", err, tt.expectedError)
			}

			// Check error message if error is expected
			if tt.expectedError {
				if err == nil {
					t.Error("expected error but got nil")
				} else if err.Error() != tt.expectedErrMsg {
					t.Errorf("error message = %q, want %q", err.Error(), tt.expectedErrMsg)
				}
			}

			if stats := camera.Stats(); stats != tt.expectedStats {
				t.Errorf("Stats = %v, want %v", stats, tt.expectedStats)
			}
		})
	}
}
