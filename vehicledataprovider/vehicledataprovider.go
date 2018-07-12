package vehicledataprovider

import (
	"errors"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"gitpct.epam.com/epmd-aepr/aos_vis/visdataadapter"
)

//SubscriptionOutputData struct to inform aboute data change by subscription
type SubscriptionOutputData struct {
	ID      string
	OutData interface{}
}

type visInternalData struct {
	path          string
	data          interface{}
	id            int32
	isInitialized bool
}

type subscriptionElement struct {
	subsChan chan<- SubscriptionOutputData
	ids      []subscriptionPare
}

type subscriptionPare struct {
	subscriptionID uint64
	value          *regexp.Regexp
}

// VehicleDataProvider interface for geeting vehicle data
type VehicleDataProvider struct {
	sensorDataChannel chan []visdataadapter.VisData
	subscription      []subscriptionElement
	visDataStorage    []visInternalData
	currentSubsID     uint64
	adapter           visdataadapter.VisDataAdapter //TODO: change to interface
}

type notificationData struct {
	subsChan chan<- SubscriptionOutputData
	id       uint64
}

var instance *VehicleDataProvider
var once sync.Once

// GetInstance  get pointer to VehicleDataProvider
func GetInstance() *VehicleDataProvider {
	once.Do(func() {
		instance = &VehicleDataProvider{}
		instance.sensorDataChannel = make(chan []visdataadapter.VisData, 100)
		instance.visDataStorage = createVisDataStorage()
		go instance.start()
		instance.adapter = visdataadapter.GetVisDataAdapter()
		go instance.adapter.StartGettingData(instance.sensorDataChannel)

	})
	return instance
}

func (dataprovider *VehicleDataProvider) start() {
	for {
		incomingData := <-dataprovider.sensorDataChannel
		dataprovider.processIncomingData(incomingData[:])
	}
}

func (dataprovider *VehicleDataProvider) processIncomingData(incomeData []visdataadapter.VisData) {

	type notificationPair struct {
		id   uint64
		data map[string]interface{}
	}

	type notificationElement struct {
		subsChan         chan<- SubscriptionOutputData
		notificationData []notificationPair
	}

	var notificationArray []notificationElement
	for _, data := range incomeData {
		//find element which receive in dataStorage
		log.Debug("process path = ", data.Path, " data= ", data.Data)
		wasChanged := false
		for i := range dataprovider.visDataStorage {
			log.Debug("Check vStorage path = ", dataprovider.visDataStorage[i].path, " data= ", dataprovider.visDataStorage[i].data)

			if dataprovider.visDataStorage[i].path == data.Path {
				dataprovider.visDataStorage[i].isInitialized = true
				valueType := reflect.TypeOf(data.Data).Kind()
				log.Debug("value type ", valueType)
				log.Debug("Path was found ", data.Path)
				if valueType == reflect.Array || valueType == reflect.Slice {
					//TODO: add array comeration
					wasChanged = true
					dataprovider.visDataStorage[i].data = data.Data
				} else {
					if dataprovider.visDataStorage[i].data != data.Data {
						log.Debug("Data for ", data.Path, " was changed from ", dataprovider.visDataStorage[i].data, " to ", data.Data)
						dataprovider.visDataStorage[i].data = data.Data

						wasChanged = true
					}
				}
				break
			}
		}

		log.Debug("wasChanged = ", wasChanged)
		if wasChanged == true {
			// prepare data fro change notification
			notifyData := dataprovider.getNotificationElementsByPath(data.Path)
			for _, notifyElement := range notifyData {
				//go across all channels
				wasChFound := false
				for j := range notificationArray {
					if notificationArray[j].subsChan == notifyElement.subsChan {
						wasIDFound := false
						///go across all Id
						for k := range notificationArray[j].notificationData {
							if notificationArray[j].notificationData[k].id == notifyElement.id {
								wasIDFound = true
								notificationArray[j].notificationData[k].data[data.Path] = data.Data
								log.Debug("Add notification to abaliable ID ")
								break
							}
						}
						if wasIDFound == false {
							//Add noe ID to available channel
							log.Debug("Create new notification element ID for available channel ")
							pair := notificationPair{id: notifyElement.id, data: make(map[string]interface{})}
							pair.data[data.Path] = data.Data
							notificationArray[j].notificationData = append(notificationArray[j].notificationData, pair)
						}
						wasChFound = true
					}
				}
				if wasChFound == false {
					//add new channel
					log.Debug("Create new channel")
					pair := notificationPair{id: notifyElement.id, data: make(map[string]interface{})}
					pair.data[data.Path] = data.Data
					pairStore := []notificationPair{pair}

					notificationArray = append(notificationArray, notificationElement{subsChan: notifyElement.subsChan, notificationData: pairStore})

				}
			}
		}
	}

	for i := range notificationArray {
		for j := range notificationArray[i].notificationData {
			dataTosend := &(notificationArray[i].notificationData[j])
			sendElement := SubscriptionOutputData{ID: strconv.FormatUint(dataTosend.id, 10), OutData: dataTosend.data}
			log.Debug("Send id ", sendElement.ID, " Data: ", sendElement.OutData)
			notificationArray[i].subsChan <- sendElement
		}
	}
}
func (dataprovider *VehicleDataProvider) getNotificationElementsByPath(path string) (returnData []notificationData) {
	log.Debug("getNotificationElementsByPath path= path")
	for i := range dataprovider.subscription {
		for j := range dataprovider.subscription[i].ids {
			if dataprovider.subscription[i].ids[j].value.MatchString(path) {
				log.Debug("Find subscription element ID ", dataprovider.subscription[i].ids[j].subscriptionID, " path= ", path)
				returnData = append(returnData, notificationData{
					subsChan: dataprovider.subscription[i].subsChan,
					id:       dataprovider.subscription[i].ids[j].subscriptionID})

				break
			}
		}
	}
	return returnData
}

// IsPublicPath if path is public no authentication is required
func (dataprovider *VehicleDataProvider) IsPublicPath(path string) bool {
	if path == "Attribute.Vehicle.VehicleIdentification.VIN" {
		return true
	}
	if path == "Attribute.Vehicle.UserIdentification.Users" {
		return true
	}

	if path == "Signal.Drivetrain.InternalCombustionEngine.Power" {
		return true
	}
	return true //TODO: currently make all public
}

// GetDataByPath get vehicle data by path
func (dataprovider *VehicleDataProvider) GetDataByPath(path string) (outoutData interface{}, err error) {
	var wasFound bool
	wasFound = false
	err = nil
	validID, err := createRegexpFromPath(path)
	if err != nil {
		log.Error("Incorrect path ", err)
		return outoutData, errors.New("404 Not found")
	}
	//var outputArray []map[string]interface{}
	m := make(map[string]interface{})

	for _, data := range dataprovider.visDataStorage {
		if validID.MatchString(data.path) == true {
			wasFound = true
			if data.isInitialized == false {
				//TODO: request data from adapter
			}
			//var m map[string]interface{}

			m[data.path] = data.data
			//		outputArray = append(outputArray, m)

			log.Debug("data = ", m[data.path])
		}
	}
	if wasFound == false {
		err = errors.New("404 Not found")
	}
	//TODO : return one value, or array or object
	//outoutData = outputArray
	outoutData = m
	return outoutData, err
}

// RegestrateSubscriptionClient TODO
func (dataprovider *VehicleDataProvider) RegestrateSubscriptionClient(subsChan chan<- SubscriptionOutputData, path string) (id string, err error) {
	//TODO: add checking available path
	var subsElement subscriptionPare

	subsElement.value, err = createRegexpFromPath(path)
	if err != nil {
		log.Error("incorrect path ", err)
		return "", errors.New("404 Not found")
	}
	dataprovider.currentSubsID++

	subsElement.subscriptionID = dataprovider.currentSubsID
	var wasFound bool
	for i := range dataprovider.subscription {
		if dataprovider.subscription[i].subsChan == subsChan {
			wasFound = true
			dataprovider.subscription[i].ids = append(dataprovider.subscription[i].ids, subsElement)
			log.Debug("Add subscription to available channel ID", dataprovider.currentSubsID, " path ", path)
		}
	}

	if wasFound == false {
		var subscripRootElement subscriptionElement
		subscripRootElement.subsChan = subsChan
		subscripRootElement.ids = append(subscripRootElement.ids, subsElement)
		dataprovider.subscription = append(dataprovider.subscription, subscripRootElement)
		log.Debug("Create new subscription ID", dataprovider.currentSubsID, " path ", path)
	}
	return strconv.FormatUint(dataprovider.currentSubsID, 10), nil
}

// RegestrateUnSubscription TODO
func (dataprovider *VehicleDataProvider) RegestrateUnSubscription(subsChan chan<- SubscriptionOutputData, subsID string) (err error) {
	err = nil
	if subsID != "1111" {
		err = errors.New("404 Not found")
	}
	return err
}

// RegestrateUnSubscribAll TODO
func (dataprovider *VehicleDataProvider) RegestrateUnSubscribAll(subsChan chan<- SubscriptionOutputData) (err error) {
	return nil
}

func createVisDataStorage() []visInternalData {
	var storage []visInternalData
	element := visInternalData{path: "Attribute.Vehicle.UserIdentification.Users", id: 8888, data: []string{"User1"}, isInitialized: true}
	storage = append(storage, element)
	element = visInternalData{path: "Attribute.Vehicle.VehicleIdentification.VIN", id: 39, data: "1234567890QWERTYU", isInitialized: true}
	storage = append(storage, element)
	element = visInternalData{path: "Signal.Drivetrain.InternalCombustionEngine.RPM", id: 58, data: 2372, isInitialized: true}
	storage = append(storage, element)
	element = visInternalData{path: "Signal.Drivetrain.InternalCombustionEngine.Power", id: 65, data: 60, isInitialized: true}
	storage = append(storage, element)

	return storage
}

func createRegexpFromPath(path string) (exp *regexp.Regexp, err error) {
	regexpStr := strings.Replace(path, ".", "[.]", -1)
	regexpStr = strings.Replace(regexpStr, "*", ".*?", -1)
	regexpStr = "^" + regexpStr
	log.Debug("filter =", regexpStr)
	exp, err = regexp.Compile(regexpStr)
	return exp, err
}

func isArraysEqual(arr1, arr2 []interface{}) (result bool) {
	if arr1 == nil && arr2 == nil {
		return true
	}

	if arr1 == nil || arr2 == nil {
		return false
	}

	if len(arr1) != len(arr2) {
		return false
	}

	for i := range arr1 {
		if arr1[i] != arr2[i] {
			return false
		}
	}
	return true
}
