package vehicle

// LICENSE

// Copyright (c) 2019-2021 andig

// This module is NOT covered by the MIT license. All rights reserved.

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/provider"
	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/oauth"
	"github.com/evcc-io/evcc/util/request"
	"github.com/evcc-io/evcc/util/sponsor"
	"github.com/evcc-io/evcc/vehicle/tronity"
	"github.com/thoas/go-funk"
	"golang.org/x/oauth2"
)

// Tronity is an api.Vehicle implementation for the Tronity api
type Tronity struct {
	*Embed
	*request.Helper
	log   *util.Logger
	oc    *oauth2.Config
	vid   string
	bulkG func() (interface{}, error)
}

func init() {
	registry.Add("tronity", NewTronityFromConfig, nil)
}

//go:generate go run ../cmd/tools/decorate.go -f decorateTronity -b *Tronity -r api.Vehicle -t "api.ChargeState,Status,func() (api.ChargeStatus, error)" -t "api.VehicleOdometer,Odometer,func() (float64, error)" -t "api.VehicleStartCharge,StartCharge,func() error" -t "api.VehicleStopCharge,StopCharge,func() error"

// NewTronityFromConfig creates a new vehicle
func NewTronityFromConfig(other map[string]interface{}) (api.Vehicle, error) {
	cc := struct {
		Embed       `mapstructure:",squash"`
		Credentials ClientCredentials
		Tokens      Tokens
		VIN         string
		Cache       time.Duration
	}{
		Cache: interval,
	}

	if err := util.DecodeOther(other, &cc); err != nil {
		return nil, err
	}

	if err := cc.Credentials.Error(); err != nil {
		return nil, err
	}

	if !sponsor.IsAuthorized() {
		return nil, api.ErrSponsorRequired
	}

	// authenticated http client with logging injected to the tronity client
	log := util.NewLogger("tronity")

	oc, err := tronity.OAuth2Config(cc.Credentials.ID, cc.Credentials.Secret)
	if err != nil {
		return nil, err
	}

	v := &Tronity{
		log:    log,
		Embed:  &cc.Embed,
		Helper: request.NewHelper(log),
		oc:     oc,
	}

	var ts oauth2.TokenSource

	// https://app.platform.tronity.io/docs#tag/Authentication
	if err := cc.Tokens.Error(); err != nil {
		// use app flow if we don't have tokens
		ts = oauth.RefreshTokenSource(&oauth2.Token{}, v)
	} else {
		// use provided tokens generated by code flow
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, request.NewHelper(log).Client)
		ts = oc.TokenSource(ctx, &oauth2.Token{
			AccessToken:  cc.Tokens.Access,
			RefreshToken: cc.Tokens.Refresh,
			Expiry:       time.Now(),
		})
	}

	// replace client transport with authenticated transport
	v.Client.Transport = &oauth2.Transport{
		Source: ts,
		Base:   v.Client.Transport,
	}

	vehicles, err := v.vehicles()
	if err != nil {
		return nil, err
	}

	var vehicle tronity.Vehicle
	if cc.VIN == "" && len(vehicles) == 1 {
		vehicle = vehicles[0]
	} else {
		for _, v := range vehicles {
			if v.VIN == strings.ToUpper(cc.VIN) {
				vehicle = v
			}
		}
	}

	if vehicle.ID == "" {
		return nil, errors.New("vin not found")
	}

	v.vid = vehicle.ID
	v.bulkG = provider.NewCached(v.bulk, cc.Cache).InterfaceGetter()

	var status func() (api.ChargeStatus, error)
	if funk.ContainsString(vehicle.Scopes, tronity.ReadCharge) {
		status = v.status
	}

	var odometer func() (float64, error)
	if funk.ContainsString(vehicle.Scopes, tronity.ReadOdometer) {
		odometer = v.odometer
	}

	var start, stop func() error
	if funk.ContainsString(vehicle.Scopes, tronity.WriteChargeStartStop) {
		start = v.startCharge
		stop = v.stopCharge
	}

	return decorateTronity(v, status, odometer, start, stop), nil
}

// RefreshToken performs token refresh by logging in with app context
func (v *Tronity) RefreshToken(_ *oauth2.Token) (*oauth2.Token, error) {
	data := struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		GrantType    string `json:"grant_type"`
	}{
		ClientID:     v.oc.ClientID,
		ClientSecret: v.oc.ClientSecret,
		GrantType:    "app",
	}

	req, err := request.New(http.MethodPost, v.oc.Endpoint.TokenURL, request.MarshalJSON(data), request.JSONEncoding)
	if err != nil {
		return nil, err
	}

	var token oauth2.Token
	err = request.NewHelper(v.log).DoJSON(req, &token)

	return &token, err
}

// vehicles implements the vehicles api
func (v *Tronity) vehicles() ([]tronity.Vehicle, error) {
	uri := fmt.Sprintf("%s/v1/vehicles", tronity.URI)

	var res tronity.Vehicles
	err := v.GetJSON(uri, &res)

	return res.Data, err
}

// bulk implements the bulk api
func (v *Tronity) bulk() (interface{}, error) {
	uri := fmt.Sprintf("%s/v1/vehicles/%s/bulk", tronity.URI, v.vid)

	var res tronity.Bulk
	err := v.GetJSON(uri, &res)

	return res, err
}

// SoC implements the api.Vehicle interface
func (v *Tronity) SoC() (float64, error) {
	res, err := v.bulkG()

	if res, ok := res.(tronity.Bulk); err == nil && ok {
		return res.Level, nil
	}

	return 0, err
}

// status implements the api.ChargeState interface
func (v *Tronity) status() (api.ChargeStatus, error) {
	status := api.StatusA // disconnected
	res, err := v.bulkG()

	if res, ok := res.(tronity.Bulk); err == nil && ok {
		if res.Charging == "Charging" {
			status = api.StatusC
		}
	}

	return status, err
}

var _ api.VehicleRange = (*Tronity)(nil)

// Range implements the api.VehicleRange interface
func (v *Tronity) Range() (int64, error) {
	res, err := v.bulkG()

	if res, ok := res.(tronity.Bulk); err == nil && ok {
		return int64(res.Range), nil
	}

	return 0, err
}

// odometer implements the api.VehicleOdometer interface
func (v *Tronity) odometer() (float64, error) {
	res, err := v.bulkG()

	if res, ok := res.(tronity.Bulk); err == nil && ok {
		return res.Odometer, nil
	}

	return 0, err
}

func (v *Tronity) post(uri string) error {
	resp, err := v.Post(uri, "", nil)
	if err == nil {
		err = request.ResponseError(resp)
	}

	// ignore HTTP 405
	if err != nil {
		if err2, ok := err.(request.StatusError); ok && err2.HasStatus(http.StatusMethodNotAllowed) {
			err = nil
		}
	}

	return err
}

// startCharge implements the api.VehicleStartCharge interface
func (v *Tronity) startCharge() error {
	uri := fmt.Sprintf("%s/v1/vehicles/%s/charge_start", tronity.URI, v.vid)
	return v.post(uri)
}

// stopCharge implements the api.VehicleStopCharge interface
func (v *Tronity) stopCharge() error {
	uri := fmt.Sprintf("%s/v1/vehicles/%s/charge_stop", tronity.URI, v.vid)
	return v.post(uri)
}
