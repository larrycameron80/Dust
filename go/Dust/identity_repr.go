package Dust

import (
	"bufio"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/blanu/Dust/go/Dust/crypting"
	"github.com/blanu/Dust/go/Dust/prim"
)

var (
	ErrNoMagic          = &ParameterError{ParameterMissing, "magic line", ""}
	ErrNoAddress        = &ParameterError{ParameterMissing, "network address", ""}
	ErrNoPrivateKey     = &ParameterError{ParameterMissing, "private key", ""}
	ErrNoPublicKey      = &ParameterError{ParameterMissing, "public key", ""}
	ErrNoModelName      = &ParameterError{ParameterMissing, "model name", ""}
	ErrInvalidAddress   = &ParameterError{ParameterInvalid, "network address", ""}
	ErrInvalidModelName = &ParameterError{ParameterInvalid, "model name", ""}
	ErrSyntax           = errors.New("Dust: bad identity record syntax")
)

func parseEndpointAddress(addrString string) (*endpointAddress, error) {
	// Do all the splitting manually, because we really don't want to accidentally take a DNS lookup here.
	// Unfortunately, there's no net.ParseTCPAddr, only net.ResolveTCPAddr.
	colonIndex := strings.LastIndex(addrString, ":")
	if colonIndex == -1 {
		return nil, ErrInvalidAddress
	}

	hostString := addrString[:colonIndex]
	portString := addrString[colonIndex+1:]
	if strings.HasPrefix(hostString, "[") && strings.HasSuffix(hostString, "]") {
		hostString = hostString[1 : len(hostString)-1]
	} else if strings.IndexRune(hostString, ':') != -1 {
		// If the host part has a colon, require it to be bracketed.
		return nil, ErrInvalidAddress
	}

	ip := net.ParseIP(hostString)
	if ip == nil {
		return nil, ErrInvalidAddress
	}

	port, err := strconv.ParseUint(portString, 10, 16)
	if err != nil {
		return nil, ErrInvalidAddress
	}

	tcpAddr := &net.TCPAddr{ip, int(port), ""}
	idBytes, err := crypting.IdentityBytesOfNetworkAddress(tcpAddr)
	if err != nil {
		return nil, ErrInvalidAddress
	}

	return &endpointAddress{tcpAddr, idBytes}, nil
}

func extractModelSpec(
	params map[string]string,
	ackedParams map[string]bool,
	topKey string,
) (*modelSpec, error) {
	modelName, ok := params[topKey]
	if !ok {
		return nil, ErrNoModelName
	}
	ackedParams[topKey] = true

	subprefix := topKey + "."
	modelParams := make(map[string]string)
	for key, val := range params {
		if strings.HasPrefix(key, subprefix) {
			modelParams[key[len(subprefix):]] = val
			ackedParams[key] = true
		}
	}

	return &modelSpec{modelName, modelParams}, nil
}

func insertModelSpec(ms *modelSpec, params map[string]string, topKey string) {
	params[topKey] = ms.name
	for subkey, val := range ms.params {
		params[topKey+"."+subkey] = val
	}
}

func loadCryptingParams(
	params map[string]string,
	ackedParams map[string]bool,
) (result crypting.Params, err error) {
	result = defCryptingParams

	if mtuStr, present := params[bridgeParamMTU]; present {
		var mtu uint64
		if mtu, err = strconv.ParseUint(mtuStr, 10, 0); err != nil {
			return
		}

		ackedParams[bridgeParamMTU] = true
		result.MTU = int(mtu)
	}

	err = crypting.ValidateParams(result)
	return
}

func loadEndpointConfigBridgeLine(
	bline BridgeLine,
	ackedParams map[string]bool,
) (result *endpointConfig, err error) {
	endpointAddress, err := parseEndpointAddress(bline.Address)
	if err != nil {
		return
	}

	modelSpec, err := extractModelSpec(bline.Params, ackedParams, bridgeParamModel)
	if err != nil {
		return
	}

	cryptingParams, err := loadCryptingParams(bline.Params, ackedParams)
	if err != nil {
		return
	}

	result = &endpointConfig{
		endpointAddress: *endpointAddress,
		modelSpec:       *modelSpec,
		cryptingParams:  cryptingParams,
	}
	return
}

// LoadServerPublicBridgeLine converts parameters from a bridge line into a server public identity.
func LoadServerPublicBridgeLine(bline BridgeLine) (result *ServerPublic, err error) {
	ackedParams := make(map[string]bool)
	endpointConfig, err := loadEndpointConfigBridgeLine(bline, ackedParams)
	if err != nil {
		return
	}

	publicString, ok := bline.Params[bridgeParamPublicKey]
	if !ok {
		return nil, ErrNoPublicKey
	}
	longtermPublic, err := prim.LoadPublicText(publicString)
	if err != nil {
		return
	}
	ackedParams[bridgeParamPublicKey] = true

	err = CheckUnackedParams(bline.Params, ackedParams)
	if err != nil {
		return
	}

	result = &ServerPublic{
		nickname:       bline.Nickname,
		endpointConfig: *endpointConfig,
		longtermPublic: longtermPublic,
	}
	return
}

// BridgeLine returns a suitable bridge line for a server public identity.
func (spub ServerPublic) BridgeLine() BridgeLine {
	addrString := spub.tcpAddr.String()
	params := map[string]string{
		bridgeParamPublicKey: spub.longtermPublic.Text(),
	}

	if mtu := spub.cryptingParams.MTU; mtu != defCryptingParams.MTU {
		params[bridgeParamMTU] = strconv.FormatUint(uint64(mtu), 10)
	}

	insertModelSpec(&spub.modelSpec, params, "m")
	return BridgeLine{spub.nickname, addrString, params}
}

// LoadServerPrivateFile loads server private identity information from path.
func LoadServerPrivateFile(
	path string,
) (result *ServerPrivate, err error) {
	var file *os.File
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()

	if file, err = os.Open(path); err != nil {
		return
	}

	lines := bufio.NewScanner(file)
	scanErrorOr := func(otherError error) error {
		scanError := lines.Err()
		if scanError != nil {
			return scanError
		} else {
			return otherError
		}
	}

	if !lines.Scan() {
		return nil, scanErrorOr(ErrSyntax)
	}
	if lines.Text() != magicLine {
		return nil, ErrNoMagic
	}

	if !lines.Scan() {
		return nil, scanErrorOr(ErrSyntax)
	}
	nickname := lines.Text()

	if !lines.Scan() {
		return nil, scanErrorOr(ErrNoAddress)
	}
	addrLine := lines.Text()
	endpointAddress, err := parseEndpointAddress(addrLine)
	if err != nil {
		return
	}

	if !lines.Scan() {
		return nil, scanErrorOr(ErrNoPrivateKey)
	}
	privateLine := lines.Text()
	private, err := prim.LoadPrivateText(privateLine)
	if err != nil {
		return
	}

	params := make(map[string]string)
	for lines.Scan() {
		paramLine := lines.Text()
		equals := strings.IndexRune(paramLine, '=')
		if equals == -1 {
			return nil, ErrSyntax
		}

		key, val := paramLine[:equals], paramLine[equals+1:]
		params[key] = val
	}

	err = lines.Err()
	if err != nil {
		return
	}

	ackedParams := make(map[string]bool)
	modelSpec, err := extractModelSpec(params, ackedParams, bridgeParamModel)
	if err != nil {
		return
	}

	cryptingParams, err := loadCryptingParams(params, ackedParams)
	if err != nil {
		return
	}

	err = CheckUnackedParams(params, ackedParams)
	if err != nil {
		return
	}

	result = &ServerPrivate{
		nickname: nickname,
		endpointConfig: endpointConfig{
			endpointAddress: *endpointAddress,
			modelSpec:       *modelSpec,
			cryptingParams:  cryptingParams,
		},
		longtermPrivate: private,
	}
	return
}

// SavePrivateFile saves the given server private identity information to a new file named path.  The file
// must not already exist.
func (spriv ServerPrivate) SavePrivateFile(path string) error {
	headerLines := []string{
		magicLine,
		spriv.nickname,
		spriv.tcpAddr.String(),
		spriv.longtermPrivate.PrivateText(),
	}

	for _, line := range headerLines {
		if strings.ContainsAny(line, "\r\n") {
			return ErrSyntax
		}
	}

	paramLines := []string{}
	for key, val := range spriv.Public().BridgeLine().Params {
		if strings.ContainsAny(key, "\r\n") || strings.ContainsAny(val, "\r\n") {
			return ErrSyntax
		}

		switch key {
		case bridgeParamPublicKey:
			// Don't save public key; it's inferred from the private key.
		default:
			paramLines = append(paramLines, key+"="+val)
		}
	}

	allLines := append([]string{}, headerLines...)
	allLines = append(allLines, paramLines...)
	allLines = append(allLines, "")
	contentString := strings.Join(allLines, "\n")

	var file *os.File
	var err error
	commit := false
	defer func() {
		if file != nil {
			_ = file.Close()
			if !commit {
				_ = os.Remove(path)
			}
		}
	}()

	if file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); err != nil {
		return err
	}

	if _, err = file.Write([]byte(contentString)); err != nil {
		return err
	}

	if err = file.Sync(); err != nil {
		return err
	}

	if err := file.Close(); err != nil {
		file = nil
		_ = os.Remove(path)
		return err
	}

	commit = true
	return nil
}
