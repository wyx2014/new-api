package system_setting

import "github.com/QuantumNous/new-api/common"

var ServerAddress = "http://localhost:3000" + common.ModelGatewayPrefix
var TaskPublicAddress = ""
var WorkerUrl = ""
var WorkerValidKey = ""
var WorkerAllowHttpImageRequestEnabled = false

func EnableWorker() bool {
	return WorkerUrl != ""
}
