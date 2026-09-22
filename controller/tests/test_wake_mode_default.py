"""
The wake word default moved to private listening for NEW installs only
(docs/listening.md). Defaults are layered under the stored fleet config, so the
default alone would switch every existing fleet that never saved the key — v25
pins "off" into those, and nothing else.
"""

import json
import sqlite3

import em_db


def _fleet(path):
    c = sqlite3.connect(path)
    try:
        row = c.execute("SELECT value FROM system_config "
                        "WHERE key = 'global_device_config'").fetchone()
        return json.loads(row[0])
    finally:
        c.close()


def _rewind_to_v24(path, fleet_cfg):
    c = sqlite3.connect(path)
    try:
        c.execute("UPDATE system_config SET value = ? WHERE key = 'global_device_config'",
                  (json.dumps(fleet_cfg),))
        c.execute("UPDATE system_config SET value = '24' WHERE key = 'schema_version'")
        c.commit()
    finally:
        c.close()


def test_a_new_install_listens_privately(tmp_path):
    p = str(tmp_path / "em.db")
    em_db.init(p)
    assert _fleet(p)["owwOnDevice"] == "on"
    assert em_db.get_global_device_config()["owwOnDevice"] == "on"


def test_an_existing_fleet_that_never_chose_keeps_controller_wake(tmp_path):
    p = str(tmp_path / "em.db")
    em_db.init(p)
    _rewind_to_v24(p, {"owwThreshold": 0.5})   # predates the key
    em_db.init(p)
    assert _fleet(p)["owwOnDevice"] == "off"
    assert em_db.get_global_device_config()["owwOnDevice"] == "off"


def test_an_existing_fleet_that_chose_is_left_alone(tmp_path):
    for chosen in ("on", "shadow", "off"):
        p = str(tmp_path / f"em-{chosen}.db")
        em_db.init(p)
        _rewind_to_v24(p, {"owwOnDevice": chosen})
        em_db.init(p)
        assert _fleet(p)["owwOnDevice"] == chosen
