/* SPDX-License-Identifier: GPL-2.0-only
 * Independently authored Biscuit/Radar 80Hz capture high-pass.
 * Include after tlv320aic3101.h and before the driver's controls array.
 * No firmware upload, arbitrary register API, AGC, gain or clock changes.
 */
#ifndef TATER_ADC3101_HPF_H
#define TATER_ADC3101_HPF_H

#ifndef TATER_ADC_HPF80_DEFAULT
#define TATER_ADC_HPF80_DEFAULT 1
#endif

static bool tater_hpf80 = TATER_ADC_HPF80_DEFAULT;
module_param_named(tater_hpf80, tater_hpf80, bool, 0444);
MODULE_PARM_DESC(tater_hpf80, "TATER_ADC_HPF80_V1: Puffin 80Hz capture HPF; boot-time only, default on");
static atomic_t tater_hpf_verified = ATOMIC_INIT(0);

static int tater_hpf_info(struct snd_kcontrol *control,
			  struct snd_ctl_elem_info *info)
{
	(void)control;
	info->type = SNDRV_CTL_ELEM_TYPE_INTEGER;
	info->count = 4;
	info->value.integer.min = 0;
	info->value.integer.max = 16000;
	return 0;
}

static int tater_hpf_get(struct snd_kcontrol *control,
			 struct snd_ctl_elem_value *value)
{
	int mask = atomic_read(&tater_hpf_verified);
	(void)control;
	value->value.integer.value[0] = 1; /* interface schema */
	value->value.integer.value[1] = mask == 255 ? 80 : 0;
	value->value.integer.value[2] = mask;
	value->value.integer.value[3] = mask == 255 ? 16000 : 0;
	return 0;
}

/* Caller holds codecMutex across the complete transaction. Its selected-page
 * cache is updated and restored; never bypass this lock from a userspace I2C
 * utility while ALSA is running. Only fixed scalar/first-order pages are used.
 */
static int tater_hpf_page(struct aic31xx_priv *priv, unsigned int adc, u8 page)
{
	u8 data[2] = { 0, page };
	if (priv->adc_page_no[adc] == page)
		return 0;
	if (i2c_master_send(priv->adc_control_data[adc], data, 2) != 2)
		return -EIO;
	priv->adc_page_no[adc] = page;
	return 0;
}

static int tater_hpf_read(struct aic31xx_priv *priv, unsigned int adc,
			  u8 page, u8 reg, u8 *data, unsigned int len)
{
	struct i2c_client *client = priv->adc_control_data[adc];
	struct i2c_msg messages[2];
	if (!client || !len || len > 6 || tater_hpf_page(priv, adc, page))
		return -EIO;
	messages[0].addr = client->addr;
	messages[0].flags = 0;
	messages[0].len = 1;
	messages[0].buf = &reg;
	messages[1].addr = client->addr;
	messages[1].flags = I2C_M_RD;
	messages[1].len = len;
	messages[1].buf = data;
	return i2c_transfer(client->adapter, messages, 2) == 2 ? 0 : -EIO;
}

static int tater_hpf_write(struct aic31xx_priv *priv, unsigned int adc,
			   u8 reg, const u8 values[6])
{
	u8 payload[7];
	if (tater_hpf_page(priv, adc, 4))
		return -EIO;
	payload[0] = reg;
	memcpy(payload + 1, values, 6);
	return i2c_master_send(priv->adc_control_data[adc], payload, 7) == 7 ? 0 : -EIO;
}

static int tater_hpf_apply(struct snd_soc_codec *codec, bool diagnostic_mode)
{
	/* Generated from the published TI first-order transfer function, NOT
	 * copied vendor tuning. Q1.15 N0,N1,D1 = 32261,-32261,31755.
	 */
	static const u8 candidate[6] = { 0x7e, 0x05, 0x81, 0xfb, 0x7c, 0x0b };
	struct aic31xx_priv *priv = snd_soc_codec_get_drvdata(codec);
	u8 saved[8][6], pages[4], readback[6], state;
	unsigned int adc, channel, touched = 0;
	int error = 0, rollback_error = 0;

	atomic_set(&tater_hpf_verified, 0);
	if (!tater_hpf80)
		return 0; /* normal kernels retain the exact existing tuning */
	if (!priv || NUM_ADC3101 != 4 || diagnostic_mode || priv->freq_out != 16000 ||
	    (strcmp(CONFIG_ARCH_MTK_PROJECT, "biscuit") && strcmp(CONFIG_ARCH_MTK_PROJECT, "radar")))
		return -EINVAL;
	for (adc = 0; adc < 4; adc++)
		if (!priv->adc_control_data[adc])
			return -ENODEV;

	mutex_lock(&priv->codecMutex);
	for (adc = 0; adc < 4; adc++)
		pages[adc] = priv->adc_page_no[adc];
	/* Only during set_pll's stopped-ADC phase, before hw_params powers it.
	 * Never rewrite coefficients while the continuous wake capture is live.
	 */
	for (adc = 0; adc < 4; adc++) {
		error = tater_hpf_read(priv, adc, 0, 18, &state, 1);
		if (error || (state & 128)) { error = error ? error : -EBUSY; goto restore_pages; }
		error = tater_hpf_read(priv, adc, 0, 61, &state, 1);
		if (error || state != 2) { error = error ? error : -EINVAL; goto restore_pages; }
		error = tater_hpf_read(priv, adc, 0, 86, &state, 1);
		if (error || (state & 128)) { error = error ? error : -EINVAL; goto restore_pages; }
		error = tater_hpf_read(priv, adc, 0, 94, &state, 1);
		if (error || (state & 128)) { error = error ? error : -EINVAL; goto restore_pages; }
	}
	for (channel = 0; channel < 8; channel++) {
		error = tater_hpf_read(priv, channel / 2, 4, channel % 2 ? 72 : 8, saved[channel], 6);
		if (error) goto restore_pages;
	}
	for (channel = 0; channel < 8; channel++) {
		touched = channel + 1; /* a failed transfer may have partly written */
		error = tater_hpf_write(priv, channel / 2, channel % 2 ? 72 : 8, candidate);
		if (error) goto rollback;
	}
	for (channel = 0; channel < 8; channel++) {
		error = tater_hpf_read(priv, channel / 2, 4, channel % 2 ? 72 : 8, readback, 6);
		if (!error && memcmp(readback, candidate, 6)) error = -EIO;
		if (error) goto rollback;
	}
	goto restore_pages;

rollback:
	while (touched) {
		channel = --touched;
		if (tater_hpf_write(priv, channel / 2, channel % 2 ? 72 : 8, saved[channel]))
			rollback_error = -EIO;
		else if (tater_hpf_read(priv, channel / 2, 4, channel % 2 ? 72 : 8, readback, 6) ||
			 memcmp(readback, saved[channel], 6))
			rollback_error = -EIO;
	}
restore_pages:
	for (adc = 0; adc < 4; adc++)
		if (tater_hpf_page(priv, adc, pages[adc]))
			rollback_error = -EIO;
	mutex_unlock(&priv->codecMutex);
	if (rollback_error) {
		dev_err(codec->dev, "Tater HPF restore failed; refusing capture\n");
		return rollback_error;
	}
	if (!error)
		atomic_set(&tater_hpf_verified, 255);
	return error;
}

#endif
