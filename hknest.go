package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/brutella/hc"
	"github.com/brutella/hc/accessory"
	"github.com/brutella/hc/characteristic"
	"github.com/brutella/hc/service"
	"github.com/brutella/log"

	"github.com/ablyler/nest"
)

const (
	AlarmOk        = "ok"
	AlarmWarning   = "warning"
	AlarmEmergency = "emergency"
)

type HKThermostat struct {
	accessory *accessory.Accessory
	transport hc.Transport

	thermostat *accessory.Thermostat
}

type HKSmokeCoAlarm struct {
	accessory *accessory.Accessory
	transport hc.Transport
}

var (
	thermostats      map[string]*HKThermostat
	smokeCoDetectors map[string]*HKSmokeCoAlarm
	structuresChan   chan map[string]*nest.Structure
	nestPin          string
	nestToken        string
	homekitPin       string
	productId        string
	productSecret    string
	state            string
)

func logEvent(device *nest.SmokeCoAlarm) {
	data, _ := json.MarshalIndent(device, "", "  ")
	fmt.Println(string(data))
}

func Connect() {
	client := nest.New(productId, state, productSecret, nestPin)
	client.Token = nestToken
	if nestToken == "" {
		client.Authorize()
	}
	fmt.Printf("This is the client token: ")
	fmt.Println(client.Token)

	client.DevicesStream(func(devices *nest.Devices, err error) {
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		for _, device := range devices.SmokeCoAlarms {
			hkSmokeCODetector := GetHKSmokeAlarm(device)
			services := hkSmokeCODetector.accessory.Services
			fmt.Printf("Services: %v\n", services)
			// smokeState := characteristic.NewCharacteristic(characteristic.TypeSmokeDetected)
			// coState := characteristic.NewCharacteristic(characteristic.TypeCarbonMonoxideDetected)
			//
			// switch device.CoAlarmState {
			// case AlarmOk:
			// 	coState.UpdateValue(characteristic.CarbonMonoxideDetectedCOLevelsNormal)
			// case AlarmWarning, AlarmEmergency:
			// 	coState.UpdateValue(characteristic.CarbonMonoxideDetectedCOLevelsAbnormal)
			// }
			//
			// switch device.SmokeAlarmState {
			// case AlarmOk:
			// 	smokeState.UpdateValue(characteristic.SmokeDetectedSmokeNotDetected)
			// case AlarmWarning, AlarmEmergency:
			// 	smokeState.UpdateValue(characteristic.SmokeDetectedSmokeDetected)
			// }
			//
			// for _, svc := range services {
			// 	switch svc.Type {
			// 	case service.TypeCarbonMonoxideSensor:
			// 		svc.AddCharacteristic(coState)
			// 	case service.TypeSmokeSensor:
			// 		svc.AddCharacteristic(smokeState)
			// 	}
			// }
		}
	})
}

func GetHKSmokeAlarm(nestSmokeAlarm *nest.SmokeCoAlarm) *HKSmokeCoAlarm {
	log.Printf("[INFO] Creating New HKSmokeDetector for %s", nestSmokeAlarm.Name)

	hkSmokeCoAlarm, found := smokeCoDetectors[nestSmokeAlarm.DeviceID]
	if found {
		return hkSmokeCoAlarm
	}

	info := accessory.Info{
		Name:         "Protect",
		Manufacturer: "Nest",
		Model:        "Protect",
		SerialNumber: "Numbers",
	}

	smokeCoDetector := accessory.New(info, accessory.TypeSensor)
	smokeDectection := service.NewSmokeSensor()
	coDetection := service.NewCarbonMonoxideSensor()

	smokeDectection.AddCharacteristic(characteristic.NewBatteryLevel().Characteristic)

	smokeCoDetector.AddService(smokeDectection.Service)
	smokeCoDetector.AddService(coDetection.Service)

	config := hc.Config{Pin: "00102003"}
	transport, err := hc.NewIPTransport(config, smokeCoDetector)
	if err != nil {
		log.Fatal("Failed to connect to HC transport", err)
	}

	hc.OnTermination(func() {
		<-transport.Stop()
	})

	go func() {
		transport.Start()
	}()

	hkSmokeCoAlarm = &HKSmokeCoAlarm{smokeCoDetector, transport}
	smokeCoDetectors[nestSmokeAlarm.DeviceID] = hkSmokeCoAlarm

	return hkSmokeCoAlarm
}

func GetHKThermostat(nestThermostat *nest.Thermostat) *HKThermostat {
	hkThermostat, found := thermostats[nestThermostat.DeviceID]
	if found {
		return hkThermostat
	}

	log.Printf("[INFO] Creating New HKThermostat for %s", nestThermostat.Name)

	info := accessory.Info{
		Name:         nestThermostat.Name,
		Manufacturer: "Nest",
	}

	thermostat := accessory.NewThermostat(info, float64(nestThermostat.AmbientTemperatureC), 9, 32, float64(0.5))

	config := hc.Config{Pin: homekitPin}
	transport, err := hc.NewIPTransport(config, thermostat.Accessory)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		transport.Start()
	}()

	hkThermostat = &HKThermostat{thermostat.Accessory, transport, thermostat}
	thermostats[nestThermostat.DeviceID] = hkThermostat

	thermostat.Thermostat.TargetTemperature.OnValueRemoteUpdate(func(target float64) {
		log.Printf("[INFO] Changed Target Temp for %s", nestThermostat.Name)
		nestThermostat.SetTargetTempC(float32(target))
	})

	thermostat.Thermostat.TargetHeatingCoolingState.OnValueRemoteUpdate(func(mode int) {
		log.Printf("[INFO] Changed Mode for %s", nestThermostat.Name)

		if mode == characteristic.CurrentHeatingCoolingStateHeat {
			nestThermostat.SetHvacMode(nest.Heat)
		} else if mode == characteristic.CurrentHeatingCoolingStateCool {
			nestThermostat.SetHvacMode(nest.Cool)
		} else if mode == characteristic.CurrentHeatingCoolingStateOff {
			nestThermostat.SetHvacMode(nest.Off)
		} else {
			nestThermostat.SetHvacMode(nest.HeatCool)
		}
	})

	return hkThermostat
}

func main() {
	thermostats = map[string]*HKThermostat{}
	smokeCoDetectors = map[string]*HKSmokeCoAlarm{}
	structuresChan = make(chan map[string]*nest.Structure)

	productIdArg := flag.String("product-id", "", "Nest provided product id")
	productSecretArg := flag.String("product-secret", "", "Nest provided product secret")
	stateArg := flag.String("state", "", "A value you create, used during OAuth")
	nestPinArg := flag.String("nest-pin", "", "PIN generated from the Nest site")
	nestTokenArg := flag.String("nest-token", "", "Authorization token from nest auth.")
	homekitPinArg := flag.String("homekit-pin", "", "PIN you create to be used to pair Nest with HomeKit")
	verboseArg := flag.Bool("v", false, "Whether or not log output is displayed")

	flag.Parse()

	fmt.Printf("inside let's go!\n")

	productId = *productIdArg
	productSecret = *productSecretArg
	state = *stateArg
	nestPin = *nestPinArg
	nestToken = *nestTokenArg
	homekitPin = *homekitPinArg

	if !*verboseArg {
		log.Info = false
		log.Verbose = false
	}

	hc.OnTermination(func() {
		os.Exit(1)
	})

	fmt.Printf("about to connect\n")
	Connect()
}
