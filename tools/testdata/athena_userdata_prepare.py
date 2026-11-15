# MIT-licensed TECHO5 installer fixture, from Tater Echo Linux af8de59.
# Copyright (c) 2026 TECHO5 contributors.
# Full MIT notice: ../../factory/checkers-linux/tools/techo5/LICENSE.
# Factory bundles also retain TECHO5-LICENSE and TECHO5-NOTICE.
def prepare_tater_userdata(adb, ud, factory_reset):
    """Keep setup by default, but never keep an older application OTA over a factory image."""
    if factory_reset:
        paths = [
            ud + '/local/etc/tater',
            ud + '/local/share/tater',
            ud + '/tater-linux',
            ud + '/misc/techo5',
            ud + '/techo5-linux',
            ud + '/techo5/misc/techo5',
            ud + '/techo5/techo5-linux',
        ]
        marker = 'TATER-SETUP-CLEARED'
    else:
        paths = [
            ud + '/tater-linux/app',
            ud + '/tater-linux/app.new',
            ud + '/techo5-linux/app',
            ud + '/techo5-linux/app.new',
            ud + '/techo5/techo5-linux/app',
            ud + '/techo5/techo5-linux/app.new',
        ]
        marker = 'TATER-APP-SLOTS-CLEARED'
    answer = adb.sh('rm -rf %s; sync; echo %s' % (' '.join(quote(path) for path in paths), marker))
    if marker not in answer:
        fail('could not prepare userdata for the factory install: %s' % answer)
