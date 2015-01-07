package Dust

import (
	"io"
	"time"
)

type shaperReader struct {
	recycleChan chan int
	advanceChan chan int
	heldError error
	readFrom io.Reader
	sharedBuf []byte
}

const (
	shaperBufSize = 1024
)

func newShaperReader(readFrom io.Reader, sharedBuf []byte) *shaperReader {
	return &shaperReader{
		// These have to be unbuffered for memory-model guarantees.
		recycleChan: make(chan int, 0),
		advanceChan: make(chan int, 0),
		heldError: nil,
		readFrom: readFrom,
		sharedBuf: sharedBuf,
	}
}

func (sr *shaperReader) run() {
	defer func() {
		ReportExitTo(&sr.heldError)
		close(sr.advanceChan)
	}()

	for {
		offset, ok := <-sr.recycleChan
		if !ok {
			return
		}
		// We now own the shared buffer.

		n, err := sr.readFrom.Read(sr.sharedBuf[offset:])
		if err != nil {
			sr.heldError = err
			return
		}

		sr.advanceChan <- n
	}
}

func (sr *shaperReader) cycle(offset int) {
	sr.recycleChan <- offset
}

func (sr *shaperReader) stop() {
	close(sr.recycleChan)
}

type shaperTimer struct {
	durationChan chan time.Duration
	timingChan chan time.Time
	heldError error
}

func newShaperTimer() *shaperTimer {
	return &shaperTimer{
		durationChan: make(chan time.Duration, 1),
		timingChan: make(chan time.Time, 1),
	}
}

func (st *shaperTimer) run() {
	defer func() {
		ReportExitTo(&st.heldError)
		close(st.timingChan)
	}()
	
	st.timingChan <- time.Now()
	for {
		dur, ok := <-st.durationChan
		if !ok {
			return
		}
			
		time.Sleep(dur)
		st.timingChan <- time.Now()
	}
}

func (st *shaperTimer) cycle(dur time.Duration) {
	st.durationChan <- dur
}

func (st *shaperTimer) stop() {
	close(st.durationChan)
}

type Shaper struct {
	crypto *CryptoSession
	shapedIn io.Reader
	shapedOut io.Writer
	heldError error

	reader *shaperReader
	decodeModel DecodeModel
	inBuf []byte
	
	timer *shaperTimer
	encodeModel EncodeModel
	outBuf []byte
	outPending []byte
	pullBuf []byte
	
	controlChan chan int
	statusChan chan int
}

func (sh *Shaper) handleRead(subn int) error {
	// We own inBuf until we cycle the reader again.
	decoded := sh.decodeModel.DecodeBytes(sh.inBuf[:subn])
	_, err := sh.crypto.PushRead(decoded)
	if err != nil {
		return err
	}

	sh.reader.cycle(0)
	return nil
}

func (sh *Shaper) handleTimer() error {
	outLen := sh.encodeModel.NextPacketLength()
	sh.timer.cycle(sh.encodeModel.NextPacketSleep())

	// TODO: probably slow.

	outValid := 0
	outTail := sh.outBuf[:outLen]
	for len(outTail) > 0 {
		if len(sh.outPending) > 0 {
			// TODO: refactor with other copy* functions (move them out of crypto_layer
			// probably)...
			copied := copy(outTail, sh.outPending)
			outValid += copied
			outTail = outTail[copied:]
			sh.outPending = sh.outPending[copied:]
			continue
		}

		pullN, err := sh.crypto.PullWrite(sh.pullBuf)
		if err != nil && err != io.ErrNoProgress {
			return err
		}

		encoded := sh.encodeModel.EncodeBytes(sh.pullBuf[:pullN])
		copied := copy(outTail, encoded)
		outValid += copied
		outTail = outTail[copied:]
		encodedTail := encoded[copied:]
		if len(encodedTail) > 0 {
			sh.outPending = append(sh.outPending, encodedTail...)
		}

		if err != nil {
			// It was an ErrNoProgress, else we'd have returned above.
			break
		}
	}

	_, err := sh.shapedOut.Write(sh.outBuf[:outValid])
	if err != nil {
		return err
	}

	return nil
}

func (sh *Shaper) handleOneStateChange() bool {
	var err error
	select {
	case subn, ok := <-sh.reader.advanceChan:
		if !ok {
			// Reader is dead.
			return true
		}
		
		err = sh.handleRead(subn)
		if err != nil {
			sh.heldError = err
			return true
		}
		
	case _, ok := <-sh.timer.timingChan:
		if !ok {
			// Timer is dead.
			return true
		}

		err = sh.handleTimer()
		if err != nil {
			sh.heldError = err
			return true
		}

	case _, _ = <-sh.controlChan:
		// Closing the control channel is an exit request.
		return true
	}

	return false
}

func (sh *Shaper) run(afterThunk func()) {
	defer func() {
		ReportExitTo(&sh.heldError)
		sh.reader.stop()
		sh.timer.stop()
		close(sh.statusChan)
		afterThunk()
	}()

	go sh.reader.run()
	go sh.timer.run()

	sh.reader.cycle(0)

	for {
		shouldExit := sh.handleOneStateChange()
		if shouldExit {
			break
		}
	}
}

func NewShaper(
	crypto *CryptoSession,
	in io.Reader,
	decodeModel DecodeModel,
	out io.Writer,
	encodeModel EncodeModel,
) (*Shaper, error) {
	// INCOMPLETE: does not handle connection duration.
	
	sh := &Shaper{
		crypto: crypto,
		shapedIn: in,
		shapedOut: out,

		reader: nil, // initialized below
		decodeModel: decodeModel,
		inBuf: make([]byte, shaperBufSize),

		timer: nil, // initialized below
		encodeModel: encodeModel,
		outBuf: make([]byte, encodeModel.MaxPacketLength()),
		outPending: nil,
		pullBuf: make([]byte, shaperBufSize),

		heldError: nil,
		controlChan: make(chan int, 1),
		statusChan: make(chan int, 1),
	}

	sh.reader = newShaperReader(sh.shapedIn, sh.inBuf)
	sh.timer = newShaperTimer()
	return sh, nil
}

func (sh *Shaper) SpawnThen(afterThunk func()) {
	go sh.run(afterThunk)
}

func (sh *Shaper) Error() error {
	return sh.heldError
}
