// This command takes the audio input from the mic and uses
// Google Speech API to output the transcript.
//
// You need to have portaudio installed, see https://github.com/gordonklaus/portaudio
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"time"

	"bytes"

	speech "cloud.google.com/go/speech/apiv1"
	"github.com/gordonklaus/portaudio"
	"golang.org/x/net/context"
	"google.golang.org/api/option"
	"google.golang.org/api/transport"
	speechpb "google.golang.org/genproto/googleapis/cloud/speech/v1"
)

func main() {
	// Clean shutdown
	sig := make(chan os.Signal, 1)
	micStopCh := make(chan bool)
	signal.Notify(sig, os.Interrupt, os.Kill)
	go func() {
		select {
		case s := <-sig:
			fmt.Println("received signal", s, "shutting down")
			micStopCh <- true
			os.Exit(0)
		}
	}()

	// connect to the audio drivers
	portaudio.Initialize()
	defer portaudio.Terminate()

	// connect to Google for a set duration to avoid running forever
	// and charge the user a lot of money.
	runDuration := 240 * time.Second
	bgctx := context.Background()
	ctx, _ := context.WithDeadline(bgctx, time.Now().Add(runDuration))
	conn, err := transport.DialGRPC(ctx,
		option.WithEndpoint("speech.googleapis.com:443"),
		option.WithScopes("https://www.googleapis.com/auth/cloud-platform"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	client, err := speech.NewClient(ctx)
	if err != nil {
		log.Fatal(err)
	}
	stream, err := client.StreamingRecognize(ctx)
	if err != nil {
		log.Fatal(err)
	}
	// Send the initial configuration message.
	if err := stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &speechpb.StreamingRecognitionConfig{
				Config: &speechpb.RecognitionConfig{
					// Uncompressed 16-bit signed little-endian samples (Linear PCM).
					Encoding:        speechpb.RecognitionConfig_LINEAR16,
					SampleRateHertz: 16000,
					LanguageCode:    "en-US",
				},
			},
		},
	}); err != nil {
		log.Fatal(err)
	}

	go func() {
		bufIn := make([]int16, 8196)
		var bufWriter bytes.Buffer
		micstream, err := portaudio.OpenDefaultStream(1, 0, 16000, len(bufIn), bufIn)
		if err != nil {
			fmt.Println("failed to connect to the set the default stream", err)
			panic(err)
		}
		defer micstream.Close()

		if err = micstream.Start(); err != nil {
			fmt.Println("failed to connect to the input stream", err)
			panic(err)
		}
		for {
			bufWriter.Reset()
			if err := micstream.Read(); err != nil {
				fmt.Println("failed to connect to read from the default stream", err)
				panic(err)
			}
			binary.Write(&bufWriter, binary.LittleEndian, bufIn)

			if err = stream.Send(&speechpb.StreamingRecognizeRequest{
				StreamingRequest: &speechpb.StreamingRecognizeRequest_AudioContent{
					AudioContent: bufWriter.Bytes(),
				},
			}); err != nil {
				log.Printf("Could not send audio: %v", err)
			}
			select {
			case <-micStopCh:
				fmt.Println("turning off the mic")
				if err = micstream.Stop(); err != nil {
					fmt.Println("failed to stop the input stream")
				}
				return
			default:
			}
			fmt.Print(".")
		}
	}()

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Cannot stream results: %v", err)
		}
		if err := resp.Error; err != nil {
			log.Fatalf("Could not recognize: %v", err)
		}
		for _, result := range resp.Results {
			fmt.Printf("Result: %+v\n", result)
		}
	}
}
