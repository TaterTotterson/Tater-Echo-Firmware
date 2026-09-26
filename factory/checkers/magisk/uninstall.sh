#!/system/bin/sh
# Restore the stock first-run screen when the Tater Checkers module is removed.
if [ -x /data/adb/modules/tater_checkers/privacy.sh ]; then
    /system/bin/sh /data/adb/modules/tater_checkers/privacy.sh restore
fi
pm enable --user 0 com.amazon.ds2.oobe.efd >/dev/null 2>&1
cmd package clear-home-activity --user 0 com.tatertotterson.show/.MainActivity >/dev/null 2>&1
