/* SPDX-License-Identifier: GPL-2.0-only
 * Fake-bus tests of the exact disabled kernel helper. No device access.
 */
#include <assert.h>
#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

typedef uint8_t u8;
typedef struct { int value; } atomic_t;
#define ATOMIC_INIT(v) { (v) }
static int atomic_read(atomic_t *v) { return v->value; }
static void atomic_set(atomic_t *v, int value) { v->value = value; }
#define NUM_ADC3101 4
static const char *test_project = "biscuit";
#define CONFIG_ARCH_MTK_PROJECT test_project
#define SNDRV_CTL_ELEM_TYPE_INTEGER 2
#define I2C_M_RD 1
#define module_param_named(...)
#define MODULE_PARM_DESC(...)
struct snd_kcontrol { int unused; };
struct snd_ctl_elem_info {
    int type, count;
    struct { struct { int min, max; } integer; } value;
};
struct snd_ctl_elem_value { struct { struct { long value[4]; } integer; } value; };
struct i2c_adapter { int unused; };
struct i2c_client { unsigned addr; struct i2c_adapter *adapter; };
struct i2c_msg { unsigned addr, flags, len; u8 *buf; };
struct aic31xx_priv {
    struct i2c_client *adc_control_data[4];
    u8 adc_page_no[4];
    unsigned freq_out;
    int codecMutex;
};
struct snd_soc_codec { struct aic31xx_priv *priv; void *dev; };
static struct aic31xx_priv *snd_soc_codec_get_drvdata(struct snd_soc_codec *c) { return c->priv; }
static void mutex_lock(int *lock) { assert(*lock == 0); *lock = 1; }
static void mutex_unlock(int *lock) { assert(*lock == 1); *lock = 0; }
static unsigned diagnostics;
#define dev_err(...) (++diagnostics)

static u8 registers[4][5][128], bus_pages[4], saved_initial[4][5][128];
static struct i2c_adapter adapter;
static struct i2c_client clients[4];
static struct aic31xx_priv priv;
static struct snd_soc_codec codec = { &priv, NULL };
static int writes, reads, fail_channel, bad_read_channel, fail_restore;

static int i2c_master_send(struct i2c_client *client, const u8 *data, unsigned len)
{
    unsigned adc = client->addr - 0x18;
    assert(priv.codecMutex == 1 && adc < 4);
    if (len == 2 && data[0] == 0) {
        assert(data[1] == 0 || data[1] == 4);
        bus_pages[adc] = data[1];
        return 2;
    }
    /* Every coefficient write must be exactly one fixed first-order section. */
    assert(len == 7 && bus_pages[adc] == 4 && (data[0] == 8 || data[0] == 72));
    unsigned channel = adc * 2 + (data[0] == 72);
    ++writes;
    if (fail_restore && data[1] != 0x7e) return -EIO;
    if ((int)channel == fail_channel && data[1] == 0x7e) {
        memcpy(&registers[adc][4][data[0]], data + 1, 2);
        fail_channel = -1;
        return 3; /* partial successful write must also be restored */
    }
    memcpy(&registers[adc][4][data[0]], data + 1, 6);
    return 7;
}

static int i2c_transfer(struct i2c_adapter *bus, struct i2c_msg *messages, int count)
{
    assert(bus == &adapter && count == 2 && priv.codecMutex == 1);
    assert(messages[0].len == 1 && messages[0].flags == 0 && messages[1].flags == I2C_M_RD);
    unsigned adc = messages[0].addr - 0x18, reg = *messages[0].buf;
    assert(adc < 4 && messages[1].len <= 6 && messages[1].addr == messages[0].addr);
    assert((bus_pages[adc] == 0 && (reg == 18 || reg == 61 || reg == 86 || reg == 94)) ||
           (bus_pages[adc] == 4 && (reg == 8 || reg == 72)));
    ++reads;
    memcpy(messages[1].buf, &registers[adc][bus_pages[adc]][reg], messages[1].len);
    if (bus_pages[adc] == 4 && writes >= 8 &&
        (int)(adc * 2 + (reg == 72)) == bad_read_channel) {
        messages[1].buf[1] ^= 1;
        bad_read_channel = -1;
    }
    return 2;
}

#include "tater_adc3101_hpf.h"

static void reset(void)
{
    memset(&priv, 0, sizeof(priv));
    memset(registers, 0, sizeof(registers));
    memset(bus_pages, 0, sizeof(bus_pages));
    for (unsigned adc = 0; adc < 4; ++adc) {
        clients[adc].addr = 0x18 + adc;
        clients[adc].adapter = &adapter;
        priv.adc_control_data[adc] = &clients[adc];
        registers[adc][0][61] = 2;
        for (unsigned reg = 0; reg < 128; ++reg)
            registers[adc][4][reg] = (u8)(adc * 19 + reg); /* nonzero originals */
    }
    memcpy(saved_initial, registers, sizeof(registers));
    priv.freq_out = 16000;
    test_project = "biscuit";
    writes = reads = diagnostics = fail_restore = 0;
    fail_channel = bad_read_channel = -1;
    tater_hpf80 = true;
    atomic_set(&tater_hpf_verified, 0);
}

static void pages_restored(void)
{
    assert(priv.codecMutex == 0);
    for (unsigned adc = 0; adc < 4; ++adc)
        assert(bus_pages[adc] == 0 && priv.adc_page_no[adc] == 0);
}

int main(void)
{
    assert(tater_hpf80); /* Production profile is on; boot-time opt-out remains available. */
    reset();
    tater_hpf80 = false;
    assert(tater_hpf_apply(&codec, false) == 0 && !reads && !writes);
    assert(!atomic_read(&tater_hpf_verified));
    reset();
    test_project = "crown";
    assert(tater_hpf_apply(&codec, false) == -EINVAL && !reads && !writes);
    reset();
    test_project = "radar";
    assert(tater_hpf_apply(&codec, false) == 0 && writes == 8);
    reset();
    assert(tater_hpf_apply(&codec, true) == -EINVAL && !reads && !writes);
    reset();
    priv.freq_out = 48000;
    assert(tater_hpf_apply(&codec, false) == -EINVAL && !reads && !writes);
    reset();
    priv.adc_control_data[3] = NULL;
    assert(tater_hpf_apply(&codec, false) == -ENODEV && !reads && !writes);
    for (unsigned reg = 0; reg < 4; ++reg) {
        reset();
        unsigned address[] = {18, 61, 86, 94};
        registers[3][0][address[reg]] = address[reg] == 61 ? 1 : 128;
        assert(tater_hpf_apply(&codec, false) < 0 && writes == 0);
        pages_restored();
    }
    reset();
    assert(tater_hpf_apply(&codec, false) == 0 && writes == 8);
    assert(atomic_read(&tater_hpf_verified) == 255);
    pages_restored();
    struct snd_ctl_elem_info info = {0};
    struct snd_ctl_elem_value value = {0};
    assert(!tater_hpf_info(NULL, &info) && info.count == 4 && info.value.integer.max == 16000);
    assert(!tater_hpf_get(NULL, &value));
    assert(value.value.integer.value[0] == 1 && value.value.integer.value[1] == 80 &&
           value.value.integer.value[2] == 255 && value.value.integer.value[3] == 16000);
    for (unsigned channel = 0; channel < 8; ++channel) {
        reset();
        fail_channel = (int)channel;
        assert(tater_hpf_apply(&codec, false) == -EIO);
        assert(!memcmp(registers, saved_initial, sizeof(registers)));
        assert(!atomic_read(&tater_hpf_verified));
        pages_restored();
        reset();
        bad_read_channel = (int)channel;
        assert(tater_hpf_apply(&codec, false) == -EIO);
        assert(!memcmp(registers, saved_initial, sizeof(registers)));
        assert(!atomic_read(&tater_hpf_verified));
        pages_restored();
    }
    reset();
    fail_channel = 4;
    fail_restore = 1;
    assert(tater_hpf_apply(&codec, false) == -EIO && diagnostics == 1);
    assert(!atomic_read(&tater_hpf_verified));
    pages_restored();
    puts("ADC3101 HPF fake-bus tests passed: disabled, stopped/AGC/rate/IDC guards, all stereo slots, partial writes, readback and restore failures");
    return 0;
}
