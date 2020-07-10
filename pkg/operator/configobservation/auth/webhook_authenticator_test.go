package auth

import (
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/diff"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

var correctKubeConfigString = []byte(`
apiVersion: v1
kind: Config
clusters:
- name: remote-authenticator-svc
  cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUZqRENDQTNTZ0F3SUJBZ0lVUUZjYjNDMUVIOU0zUC9rZytPWmlUaG4xZ3NBd0RRWUpLb1pJaHZjTkFRRU4KQlFBd1R6RUxNQWtHQTFVRUJoTUNRMW94RURBT0JnTlZCQWdNQjAxdmNtRjJhV0V4SERBYUJnTlZCQW9NRTAxNQpJRkJ5YVhaaGRHVWdUM0puSUV4MFpDNHhFREFPQmdOVkJBTU1CMVJsYzNRZ1EwRXdIaGNOTWpBd05qQTFNRGd4Ck5qQTVXaGNOTXpBd05qQTRNRGd4TmpBNVdqQlBNUXN3Q1FZRFZRUUdFd0pEV2pFUU1BNEdBMVVFQ0F3SFRXOXkKWVhacFlURWNNQm9HQTFVRUNnd1RUWGtnVUhKcGRtRjBaU0JQY21jZ1RIUmtMakVRTUE0R0ExVUVBd3dIVkdWegpkQ0JEUVRDQ0FpSXdEUVlKS29aSWh2Y05BUUVCQlFBRGdnSVBBRENDQWdvQ2dnSUJBS3kxaml5d1hZMkZmK0N1CjdUd3pDeVFZeGdjNkMvUDYrNEI2aHhZQVdnNTJ0THlydEo0K3dJejN5OEYvbmJGTHQ0UllMelBIeTVCVlI4bFQKMXRxUGVvMVF0eEtMMjVzeXVQREJnVFNFenl6N0p4Qzg3NEFYNHVPc1luUDVVc0w3dHYzZk1tN2RXSGNaK2VTQwp0bEphTmEvNFRYbjFYRFZLSXRCVzlIUEdmMHU4anhZYXoweHNFL3pVQlh3a2ZLalcwYzBXT2tTUXZjR1VDbGtTCkpPenBiakhHRnZnL3dsVTM2Tlh0YnR0ZDhrSVBWOEh6cGNQL0E2SXVjMzB5ajV2K0U0TnBTV2FXanVBSDZHQXUKMyt6eHBDUTd1dWE5QVFYQVJ2eXFGdFNmQUtyQkNJQXZiSDhDRElwNFhPU0h3YWY5ZVQ0REJnMUpPYXg3WHdHZgo2TUFCQjBzbE4rdWZKL0Z0M04yOGNEbnowOVNIRW1SNUNnaHRoei9BZ2cwTHJsS2VORDU2WDhTR2JYemhtVnRlCjNTTGRzQWYvS3g5cGE0T3cxZkd4cVdSUnpnNmJPQVZtMGZuYW5oTGNDOEphcVpMTFZCWG5VSXJpMFRLSExXWXkKWE1veHNZeG1NaVhhRHVOOTIwaWtmbFVKTHBKZHVRNzc3QTdReUw2dUlsQkFlZytTNW80QVZhTmRZZ0Z1RkpPRwpvU1RQRmp6Tmw0dUlLOUJDckNWZmFPNk5xSDlUUFRGNmZhb3NxYXNTOGlwSlFHT1NET215UnRFWFhvYkluWm5UCkJhL1B4TVAzUWQzbjlFUHJBbG93aWRaL1JhWFJ0N2lYSmFCUEhPUWwrZjdOMkI3bllHYnkvR2RqaC83S1NaMmoKMUxFK3NCcDF5Ujhua2ZWZ3JsMW1ER0s4UmlBREFnTUJBQUdqWURCZU1CMEdBMVVkRGdRV0JCU1dWRlFSQm1LMQp1N1RBTHU3ekZ6Y1V5MjJDN3pBZkJnTlZIU01FR0RBV2dCU1dWRlFSQm1LMXU3VEFMdTd6RnpjVXkyMkM3ekFQCkJnTlZIUk1CQWY4RUJUQURBUUgvTUFzR0ExVWREd1FFQXdJQkJqQU5CZ2txaGtpRzl3MEJBUTBGQUFPQ0FnRUEKU3kxZ2JIaXJQSHZQM0c0SDZmVnAxbE1VVTZHVEk2L1NzUkxKTy9DWVdjY1dMYjBMTTZUbGp2Zk1qV0x0dDhsVgowbytVc1loUGlMZ3R3VHBjakd0ZTNvS3Z0MTFYQ2l4SU5PMTFUNGVzWkhpT3h5ZEtubTA1T3VRdGhRUkl0dlNqCkU1c0xOMGJIMlE0akQ5MHY2azVJbTRRMHVBV2F0ZllhK3JnN0ZuYUhXZkhPeDROaVp5QUxKM3VFQy9IWkJGQXkKSFhvcUlRdTZyMS81RXZmNXVVbXlSZ01EVE8yTnlES1lORENHNXQ3akdCTlRQVVBIUkNzY3JaWWRzbEZYekI2ZApIZ21QWDcvNzJ1K2FiQng3alZBK3Z0TkR4Y0xJL09sQVg0ZkxrWDBmWll0SkVNcUs0S3VETlRGR2tVaUFlbFlyCmM5cC96bW9hY1IxZG5yVnRGQ0hTY0xkbUlLZlRadlVtS2NTelF3SW1oam5na0t0bk9yUHZBbDFkc2pDamtrS3AKM0ttZk91KzRRL2ZYa2g1NTJwNTEzdHVKWTFia2ZDQm5nazk3ODRVOWlQZDZ3ajBJNVlMV3NjckJMOTVxdzkzTwpkaXpDeTFCRkFpM3R6eEdNeWt3aWpoVFU2YTh5ZlRGTXRNaGM5b0ZBMzBpZFRHaFBRaVhJalRPME1oM1drWVZKCkw1UzV6ZmZ4c1ZnV1E3Vzd6dmtMaWcwY0d4RXlXVUpsK2hFdUtGOWVxVGNIWGZEUjVqSFdXZ295NElKTWs5Ym8KNjM5Y1JsK1VnQnFVZm9jKzBRUURQWGE3WkNIcVl5U2xBWklNQm1rTzNMTWRvenhXRHhqSXV1R0ZCbmhxd3NHVQpSUHFZajJIbzhlaHhmSmFCK0hLMXBRZFBHVGNhR3hrQ1hJb3c0aXF0R09jPQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==
    server: https://remote-srv-address:6443
contexts:
- context:
    cluster: remote-authenticator-svc
    user: kubeapiserver
  name: webhook
current-context: webhook
users:
- name: kubeapiserver
  user:
    client-certificate-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUZ0akNDQTU2Z0F3SUJBZ0lVYWhzZnloakxldjA5djhRVkR1OE1Pa2NJb1Zvd0RRWUpLb1pJaHZjTkFRRUwKQlFBd1R6RUxNQWtHQTFVRUJoTUNRMW94RURBT0JnTlZCQWdNQjAxdmNtRjJhV0V4SERBYUJnTlZCQW9NRTAxNQpJRkJ5YVhaaGRHVWdUM0puSUV4MFpDNHhFREFPQmdOVkJBTU1CMVJsYzNRZ1EwRXdIaGNOTWpBd05qQTFNRGd4Ck5qRXdXaGNOTWpNd016STJNRGd4TmpFd1dqQlZNUXN3Q1FZRFZRUUdFd0pEV2pFUU1BNEdBMVVFQ0F3SFRXOXkKWVhacFlURWNNQm9HQTFVRUNnd1RUWGtnVUhKcGRtRjBaU0JQY21jZ1RIUmtMakVXTUJRR0ExVUVBd3dOYzI5dApaWGRvWlhKbExtTnZiVENDQWlJd0RRWUpLb1pJaHZjTkFRRUJCUUFEZ2dJUEFEQ0NBZ29DZ2dJQkFMR012SEt6ClJ0ZlU4cXAwTkhzWUhkc1V6UTdIRCtUSlB0NEJTVUZVQnlPM1F3NHByeEs5RmhyWFk2SWhDT2xrMUlqbDJVVysKeG96dGhoNTY3V3dGbEZVTFRKcVpxaWZ5QWdJakl5L1FFT01KS0F2RGRydjdCbk01dnlZN1JiM0k0Q3ppK2xqRwo0cCtESG01WjM3cnhod1YyckxxSUJINmVUMTEvQlNQQXNHcEJOajZtY1FHVS9waGt0YXc0MUhyYWNFTXFBSzN4CkJWb3VwYXVsd2p3YXF4Skd6RUVabEF2emxxNEtCRWtTZ2RqaWRpbkdoWEZYSmVKNXNoaFo3dDN1WG5scEhWTUYKWVB0R0JoQzkvaS9qUmljL2JPZDJ5NDNDTDZzbVJuK3FhTzR4ankyaVNvMnZRRUt5eFcwNjVDK0gzOE5vTjJSagpROXBmeFF1RWk1V3E1SUs1U1MxMTVlNCtJRW0zVDBZU0VlYmp1cUs0OVR0aUtldGFMcE1xb0h3elBzYW5lRUxWCndUYVRtWmdlbkt4ektRcnNsTlVLMUtMYUFRSVBoT245cjdlOTFsYWRRdG9RRnhHSGo5SXZEdkNma2NDdTA2V2EKMCttSjNkUDZ2TVBBN2hRbUJqU3paRDJJeXpaUmxxM3p4L2l0d3FjNW16NCt6c0s1dW1XWkZWczVzRkdsUk5zaQo5Sys4UVdhZEZqOXdLVC80ajkrODdMV1cyVnZJamhVTDVESFJqNlNWYS9LQ1NWcWI4Nkw0M1lWQStnWDRsdnNICjNjT0lZd1BoM25VZlZKS25OeExad2RycnhCeE56RTJBbkxuZ2hIL0ljYWxYKzZNTis1L080cEdUR0NZeFo1cVMKcVpxd1lycTdKZUZBNVlxaU0vN0xyYTdJT1lHbnpBaWRGZndIQWdNQkFBR2pnWU13Z1lBd0NRWURWUjBUQkFJdwpBREFSQmdsZ2hrZ0JodmhDQVFFRUJBTUNCYUF3Q3dZRFZSMFBCQVFEQWdYZ01CTUdBMVVkSlFRTU1Bb0dDQ3NHCkFRVUZCd01DTUIwR0ExVWREZ1FXQkJRY3k5RFF1NGdNV2J6S3VGdFVOdGtramovUnF6QWZCZ05WSFNNRUdEQVcKZ0JTV1ZGUVJCbUsxdTdUQUx1N3pGemNVeTIyQzd6QU5CZ2txaGtpRzl3MEJBUXNGQUFPQ0FnRUFKV3hSNEUzVApyNFowSTBDdU8xajR5SGxzZGs4ZHdRazdIbHhWV1NjdGJVUTlvZ1FHMTlCa0orV0psNUVtMGNBU1N1a2ltQmtFCjloVkdPQzI2RlluL2tZV3lGT2o0MTNaUVF2Q1FqUXNlaTJBZW1HKzhqTkx5dUNleTRGbk1BSVRLRklZSjE1YngKS1lIQ2ptQWRlLzhGblc5eWYva0ZHbTdzVW5xcXlWbjlHQmtGYldScE9zSmVScTNnb3dNL2pidmVYeFE2REZqdAo2VDdjdlZaa21ERXg3M0Z0dXU4V1Y0dXppRDhqTE50SE00SXZ5bVNtT2pxU2JCVjNFSEc1d21TaHJOT1NaR3FvCmpHUHJ4STNTWkdITnQ0MGhPQVFEYWRGbytMcmY5S09UbCtPUXR3ano4WlVod0FIV1FVZ1l3bEpVNWE1SlliV0oKYUw0MnZHVDUvL0ZyeVZRYmZMWjJKSGRjYnptUVJTUmUxbXcvNGM2Z0xHWFU2NFhoUFM3S1dyeDVWRUY1NVl1Ywpia21ESVh5cVVtOWNpWWZuODQ2blBaWlRoNFRpS2NyN1ZXYVQzYWFEbUlaKy9kRWQ2TE1qdlROZ1Bza0dXZ2M2CmNmeTFCSkxySWVJS2JJYkVMSFJOMUpaYS8vYUo0b3FyR2RyVHJmUmZ1YlI0Y1QzYzR5M1RldGNpOG5iOFlRQ28KNit6STVHN1FMMlQyeE9UOEd1UnFOK2xxNk05bzI3WFpUc1FFc0ZKa0J1bGd6SUFoR1poenFHVDRnQVRHczhYUgoxWTh0WHFFTWhRanM2VjVnQUcrYytQNWVFQVFhOHZNVTBmYkZFZmV5dEtWMlNRalQ4bEduRmp0MnhrcFRUcGJhCldldXZkME1jdlVUTXhDdERLb3lEUno0NzU5VlpQZWNQVlZRPQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==
    client-key-data: LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQpNSUlKS0FJQkFBS0NBZ0VBc1l5OGNyTkcxOVR5cW5RMGV4Z2QyeFRORHNjUDVNayszZ0ZKUVZRSEk3ZEREaW12CkVyMFdHdGRqb2lFSTZXVFVpT1haUmI3R2pPMkdIbnJ0YkFXVVZRdE1tcG1xSi9JQ0FpTWpMOUFRNHdrb0M4TjIKdS9zR2N6bS9KanRGdmNqZ0xPTDZXTWJpbjRNZWJsbmZ1dkdIQlhhc3VvZ0VmcDVQWFg4Rkk4Q3dha0UyUHFaeApBWlQrbUdTMXJEalVldHB3UXlvQXJmRUZXaTZscTZYQ1BCcXJFa2JNUVJtVUMvT1dyZ29FU1JLQjJPSjJLY2FGCmNWY2w0bm15R0ZudTNlNWVlV2tkVXdWZyswWUdFTDMrTCtOR0p6OXM1M2JMamNJdnF5WkdmNnBvN2pHUExhSksKamE5QVFyTEZiVHJrTDRmZncyZzNaR05EMmwvRkM0U0xsYXJrZ3JsSkxYWGw3ajRnU2JkUFJoSVI1dU82b3JqMQpPMklwNjFvdWt5cWdmRE0reHFkNFF0WEJOcE9abUI2Y3JITXBDdXlVMVFyVW90b0JBZytFNmYydnQ3M1dWcDFDCjJoQVhFWWVQMGk4TzhKK1J3SzdUcFpyVDZZbmQwL3E4dzhEdUZDWUdOTE5rUFlqTE5sR1dyZlBIK0szQ3B6bWIKUGo3T3dybTZaWmtWV3ptd1VhVkUyeUwwcjd4QlpwMFdQM0FwUC9pUDM3enN0WmJaVzhpT0ZRdmtNZEdQcEpWcgo4b0pKV3B2em92amRoVUQ2QmZpVyt3ZmR3NGhqQStIZWRSOVVrcWMzRXRuQjJ1dkVIRTNNVFlDY3VlQ0VmOGh4CnFWZjdvdzM3bjg3aWtaTVlKakZubXBLcG1yQml1cnNsNFVEbGlxSXovc3V0cnNnNWdhZk1DSjBWL0FjQ0F3RUEKQVFLQ0FnQXpGaVNPK0VpVkI1Ky9MZXAvMUtPYWl2U3BvTnRRNEJybnRBVUkrQTJKMkV4Q0FmcHU4TnN3WS9NMgpEajFMTW9qNHp2SGlZSTh1czVWWXBkUTB0YkpGZWhmVGtBMW1xdnFxOU1OR0daOFNSR3N5WDA2RnJpZmY5YWlyCmJuaVZyL3ZZem9Kc0V1WFlNZGtLdFc5cmtkMWcxQkhGSmlmczZzNDVQN2dSM25xa1NCazhrNVdHZUFGOWhkVEUKTmlIbEszOEx4RVVueDJiYjJQb3dWbVI1K001cVZ0YWtrS0NKZzlCQ1NYMk1MUGdSbUsrWk81YmYwc3lDeXJOVQowR09ybGMrM0xXaVRuOE9VNlVRTGd5OWxSZ2JFZWpweXF6ck1raWczSGE5QlFwNG9remY0VUc4VkwyelZCQzFNClRacWtsbnJxOWN5aVpQRjhIdnhLWVJ2c3Q1eENDY1JONk5EQjNMOGdERWUyWnhXMFhrVGV2RHA0L2hubVdCUkQKaFNrV0lDS3JLRU5jR294VUFQUUFWZVljcHJ5aUdmWHc3UXV4OWpRKzlBYmozbEpiYmhJRHlqbk9Mci9HNEtrRAp5ZzcxNFZ3aDlXS1Q3M3FRd0wzR1FoYW1NajRhVXZOdmFNTVhtOVZwMG9QaktiSE9NVVRGanRNTzRpU0ozMkxHCjFRZ1h6RWxMYTBuQU1TQUdocjhwclBVd0MzRklPSU1lQi9IZzBYUzhGMk5IMGtxdytCVmNnZVlYMFFrcDdyamUKbEcxNkJ6b3FUMitrVHZyejFIWEk1K3FXUkJQUFN0TS9MYXBJdWNRcHlvZFFnUUswRVorcysvRFdXRnBVRHc3QwpFLzRPRUZKb1ZRRW5lckszSkRjdUliaDVqb01oZE5KMlRjZnRrcGQxT24rTVRzdDFRUUtDQVFFQTU1aDg1cVNqCnBIR2VicDEzSnJZV2RSVHQ0NVZMRzhUSnpOUmxFM08xbE1tLy9hZDRIYVhaVzJjMkQ4QW1iSDY5VVhqR0R4WXkKSGp4Mll0QVkrY0VCNHp5eXRlZXFOUUFmOGRCeFIxU2hpWFVpVWUvbzRZMVdXZGQwZFRTcE1UUHpnRmNwa1pJSQpnOFhvL3QrRUR3enZXSGozRmczWkFZMUk3VDdkY0hnRjU1NGNRK0hMQ05BVnd5cWQ4Wlg5cDZ6TjNCamNJbWhoCjZQYWx0cnRpS2IwYUZORjVRQTJwdEJjYXppK0w0c2xpVUZiZnA4T3RwdHc1MVkzQWhiem5YaW9DQVZtSEt2azkKUGtDSTBkTWE0WnprVHdVMnBwSFZRTUVLTTdXbnhWM0oxRktZMHM5K1pkNGR3NEg2YW5aeE1oVmN3QW9lNFlVLwpPNHZya3JieU4zd2c1d0tDQVFFQXhFSlBDdVVhSkNmLzFEUVBOQzJZaHZWYkcvcmdlbnl4VlpuOTlWWHJuNjZICmhyMzcvTG9jM2JvcU5WUlBPT3RSOUtmSVp4UnVCUWlxeVd2ZjhpaVo2TE1PWUQyOFAvVjFCVDloRktkWUl4cU0KL2djY2poSUwreitVOGNsdFMxQnlBYkROcjJJbG5LMGxJN2hFaFh1WkZrRnZyNmtSS1VlcWlZRUc4anNQV0cvYgoybkRMankxeHJ0VlF1YjV5ck1LQWN6bEFCMldEVEgrWm1YRGF6Vi9xOEtPTmRNMUZXdjNtc1lEMEZXbWxCeVM1ClZZbHdnVHRIVWhIT2RsTm0zVUR5Vk1oMlY3cWRLVVhrdG52a1lRWFI4K1lGQUJLbkVoTVNlVGpUVzh6WEZLL1oKWUFwS1FPSnU4VzlmMzZUN0JVS1I0a1gySHhuRTNlTnhzeHZCZ0p0SDRRS0NBUUFCQlhJZmprQk9mRlhIaFJnKwphblVrNVFlN2hqQURtbFdOZXE1TGJLb3pVc1J1K09zVnJtS0wvYU9HWkVHSEh2UDB4UVNTa25WOEhxWWkvMm5zCnlBWWJHMnhxVXZBME5hRHVidzNnMDZXMnRuYUZSL3FON0JLaWFNblJybjdFZ2Nja0hMNUpMd3lza0JYYjhkNWkKTnB0amwzejNjdTR2REpGeXdtRTFtc0hqNkpXVlV3eVRLRi9BTVpMcXVzK1lpckdKcys1Y2xIdENETHhrVnVVeQo2K3VPaGZIejcxdDlPTkRjY2VjN0E4cFVNbDNnSG9QSWhaWVhzLzFTV2FmbmlXWGkzYU16OUU5cDA5MEdsOWk1CmYvaWR4SmNlR3V5RzBaTWE4VVVoSUszQUt2RVRsT2lveUZiM1FyNTQ4N2JDRXNnSzdNQ0FIQmRRU3VpcUIvWi8KZWlPbkFvSUJBRVp1UVcxNGdHUWZVcWoxc2NzWTNkYjQ4Q1JmYVBXc0QvdlhVcE1iclg1VnBOOVBDTUpPakJOcQpQc0Y2cXgrVEc0dEFOeVArNmVpMmpvdlFRY0xtblMwc0xPbU8zaUxaMUkvNGliOWV1cnVHU0xqVkZvTkpxTEVXCnhUM3IrbVAvejVvWnVBYkxveEhSOVRVWGFNZTZibHJWU3Q1d1B1OWdmNnZ1K080dkViZThGTnNVaFlpeFYwM1YKMGEyRzBpSjdmcHRiSFVaS1FNOVFMM0FvVnUxREVjNGY4NkRLRmF5czE0QTE5ZUpGVW1yNDIrWDlkN2w0NjRSaApUWVdiTXB3T05ha0ZjNnJTRnBwOE1iTG5UVE1nWXBNenBmRzd2K2MxbnZpUDB4SHJ0ZmYvajNQdTNXemhsY3poCkdqZnBQZ2hLTm81TWF5SUlIbVUrdlV2NGx2MnZQQ0VDZ2dFQkFKdEx0WklNN1lQTU8zcFNHdjdBcnpJYkovSHcKYXQ4bnZBTk1jQjEvYk45em1oWWc3OHVtaUtUc3hRTFBQVk5uK09NN0JrU0l6RFM4QjBMRm5MekVtVVZuNnBWaApVUXJIUFh1c3dCS3V0dDV3NHBIV2hEZUs2aXFnNnZnc1NXUnpxaFFWTllUTW50NVFQeHVtRGtXMUcvdm52RnNNClRnVk9PQnJBTHpMUCtVWm5zcm5uWU1DcXp6bGhPUjU4VWU5OEJWajB6b0hQR0Q0UU1HcGlTb1pOdkhMNndkQ0gKcytFdlc1czdPOWE1SnhwRGYzcUl3RTRMbjBSc2xvMHBnVTZZQU1qd25tWUpXNk5JNE5kSitMQWFZM2ZBcXNicAorSFdyY21TQ2VVaVAwMEp4NzFzbXRvQ3JBY3RkY2hsU01JMGtiOUpNS05yOWhYZWlxRzErSmxzRGZUTT0KLS0tLS1FTkQgUlNBIFBSSVZBVEUgS0VZLS0tLS0K
`)

func TestObserveWebhookTokenAuthenticator(t *testing.T) {
	tests := []struct {
		name              string
		existingConfig    map[string]interface{}
		config            *configv1.WebhookTokenAuthenticator
		configSecret      map[string][]byte
		webhookConfigured bool
		expectErrs        bool
		expectEvents      bool
		expectedSynced    map[string]string
	}{
		{
			name: "empty config",
		},
		{
			name: "referenced secret missing",
			config: &configv1.WebhookTokenAuthenticator{
				KubeConfig: configv1.SecretNameReference{
					Name: "config-secret",
				},
			},
			expectErrs: true,
		},
		{
			name: "config removal",
			existingConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"authentication-token-webhook-config-file": []interface{}{webhookTokenAuthenticatorFile},
				},
			},
			config: &configv1.WebhookTokenAuthenticator{
				KubeConfig: configv1.SecretNameReference{
					Name: "",
				},
			},
			expectEvents: true,
			expectedSynced: map[string]string{
				"secret/webhook-authenticator.openshift-kube-apiserver": "DELETE",
			},
		},
		{
			name: "correct config",
			config: &configv1.WebhookTokenAuthenticator{
				KubeConfig: configv1.SecretNameReference{
					Name: "config-secret",
				},
			},
			configSecret: map[string][]byte{
				"kubeConfig": correctKubeConfigString,
			},
			webhookConfigured: true,
			expectedSynced: map[string]string{
				"secret/webhook-authenticator.openshift-kube-apiserver": "secret/config-secret.openshift-config",
			},
			expectEvents: true,
		},
		{
			name: "same existing and observed config",
			existingConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"authentication-token-webhook-config-file": []interface{}{webhookTokenAuthenticatorFile},
				},
			},
			config: &configv1.WebhookTokenAuthenticator{
				KubeConfig: configv1.SecretNameReference{
					Name: "config-secret",
				},
			},
			configSecret: map[string][]byte{
				"kubeConfig": correctKubeConfigString,
			},
			webhookConfigured: true,
			expectedSynced: map[string]string{
				"secret/webhook-authenticator.openshift-kube-apiserver": "secret/config-secret.openshift-config",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if tt.config != nil {
				config := &configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster",
					},
					Spec: configv1.AuthenticationSpec{
						WebhookTokenAuthenticator: tt.config,
					},
				}
				if err := indexer.Add(config); err != nil {
					t.Fatal(err)
				}
			}
			if tt.configSecret != nil {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-secret",
						Namespace: "openshift-config",
					},
					Data: tt.configSecret,
				}
				if err := indexer.Add(secret); err != nil {
					t.Fatal(err)
				}
			}

			synced := map[string]string{}
			listers := configobservation.Listers{
				AuthConfigLister:    configlistersv1.NewAuthenticationLister(indexer),
				ConfigSecretLister_: corelistersv1.NewSecretLister(indexer),
				ResourceSync:        &mockResourceSyncer{t: t, synced: synced},
			}

			eventRecorder := events.NewInMemoryRecorder("webhookauthenticatortest")

			gotConfig, errs := ObserveWebhookTokenAuthenticator(listers, eventRecorder, tt.existingConfig)
			gotAuthenticator, _, err := unstructured.NestedStringSlice(gotConfig, webhookTokenAuthenticatorPath...)
			if err != nil {
				t.Fatal(err)
			}

			if tt.webhookConfigured != (len(gotAuthenticator) > 0) {
				t.Errorf("ObserveWebhookTokenAuthenticator() wanted the webhook configured: %v, but got %v", tt.webhookConfigured, gotConfig)
			}

			if recordedEvents := eventRecorder.Events(); tt.expectEvents != (len(recordedEvents) > 0) {
				t.Errorf("expected events: %v, but got %v", tt.expectEvents, recordedEvents)
			}

			if tt.expectErrs && len(errs) == 0 {
				t.Error("Expected errors.")
			}

			if !tt.expectErrs && len(errs) > 0 {
				t.Errorf("Expected 0 errors, got %v.", len(errs))
				for _, err := range errs {
					t.Log(err.Error())
				}
			}

			if !equality.Semantic.DeepEqual(tt.expectedSynced, synced) {
				t.Errorf("expected resources not synced: %s", diff.ObjectReflectDiff(tt.expectedSynced, synced))
			}
		})
	}
}

func Test_validateKubeconfigSecret(t *testing.T) {
	tests := []struct {
		name string
		data map[string][]byte
		want []string
	}{
		{
			name: "empty secret",
			want: []string{"missing required 'kubeConfig' key"},
		},
		{
			name: "wrong key",
			data: map[string][]byte{
				"sumKey": []byte("awesome stuff"),
			},
			want: []string{"missing required 'kubeConfig' key"},
		},
		{
			name: "correct key, but empty",
			data: map[string][]byte{
				"kubeConfig": []byte(""),
			},
			want: []string{"the 'kubeConfig' key is empty"},
		},
		{
			name: "correct key, but bogus data",
			data: map[string][]byte{
				"kubeConfig": []byte("you shall not parse"),
			},
			want: []string{"failed to load kubeconfig"},
		},
		{
			name: "using non-data fields",
			data: map[string][]byte{
				"kubeConfig": []byte(`
apiVersion: v1
kind: Config
clusters:
- name: remote-authenticator-svc
  cluster:
    certificate-authority: /var/certs/ca.crt
    server: https://remote-srv-address:6443
contexts:
- context:
    cluster: remote-authenticator-svc
    user: kubeapiserver
  name: webhook
current-context: webhook
preferences: {}
users:
- name: kubeapiserver
  user:
    client-certificate: /var/certs/client.crt
    client-key: /var/keys/client.key
`),
			},
			want: []string{
				"clusters[remote-authenticator-svc].certificate-authority: Invalid value: \"/var/certs/ca.crt\"",
				"users[kubeapiserver]: Required value:",
				"users[kubeapiserver].client-certificate: Invalid value: \"/var/certs/client.crt\": use \"users[kubeapiserver].client-certificate-data\" with the direct content of the file instead",
				"users[kubeapiserver].client-key: Invalid value: \"/var/keys/client.key\": use \"users[kubeapiserver].client-key-data\" with the direct content of the file instead",
			},
		},
		{
			name: "everything's fine",
			data: map[string][]byte{
				"kubeConfig": correctKubeConfigString,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret := &corev1.Secret{
				Data: tt.data,
			}
			got := validateKubeconfigSecret(secret)
			if len(got) != len(tt.want) {
				t.Errorf("validateKubeconfigSecret() = %v, want %v", got, tt.want)
				return
			}

			for i, err := range got {
				if !strings.Contains(err.Error(), tt.want[i]) {
					t.Errorf("validateKubeconfigSecret() = %v\n, want\n %v", got, tt.want)
					return
				}
			}
		})
	}
}

type mockResourceSyncer struct {
	t      *testing.T
	synced map[string]string
}

func (rs *mockResourceSyncer) SyncConfigMap(destination, source resourcesynccontroller.ResourceLocation) error {
	if (source == resourcesynccontroller.ResourceLocation{}) {
		rs.synced[fmt.Sprintf("configmap/%v.%v", destination.Name, destination.Namespace)] = "DELETE"
	} else {
		rs.synced[fmt.Sprintf("configmap/%v.%v", destination.Name, destination.Namespace)] = fmt.Sprintf("configmap/%v.%v", source.Name, source.Namespace)
	}
	return nil
}

func (rs *mockResourceSyncer) SyncSecret(destination, source resourcesynccontroller.ResourceLocation) error {
	if (source == resourcesynccontroller.ResourceLocation{}) {
		rs.synced[fmt.Sprintf("secret/%v.%v", destination.Name, destination.Namespace)] = "DELETE"
	} else {
		rs.synced[fmt.Sprintf("secret/%v.%v", destination.Name, destination.Namespace)] = fmt.Sprintf("secret/%v.%v", source.Name, source.Namespace)
	}
	return nil
}
