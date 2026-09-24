//go:build bleenroll

package bluetooth

import _ "embed"

//go:embed assets/tater-ble-enrollment.apk
var enrollmentAPK []byte

func embeddedEnrollmentAPK() []byte { return enrollmentAPK }
