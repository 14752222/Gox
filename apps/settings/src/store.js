// 应用状态：设置项 signal + 即改即存。signal 用索引取值（引擎纪律）。
import { createSignal } from "gox";
import { getStorage, setStorage } from "gx/storage";
import { setTheme } from "gx/theme";
import { merge, clampFontSize, DEFAULTS } from "./lib/prefs.js";

const KEY = "gox-settings.prefs";

const prefs = merge(getStorage(KEY));

const darkP = createSignal(prefs.dark);
const accentP = createSignal(prefs.accent);
const fontSizeP = createSignal(prefs.fontSize);
const autoSaveP = createSignal(prefs.autoSave);
const notifP = createSignal(prefs.notifications);
const soundP = createSignal(prefs.sound);
const qualityP = createSignal(prefs.quality);
const dateP = createSignal(prefs.reminderDate);

const dark = darkP[0];
const accent = accentP[0];
const fontSize = fontSizeP[0];
const autoSave = autoSaveP[0];
const notifications = notifP[0];
const sound = soundP[0];
const quality = qualityP[0];
const reminderDate = dateP[0];

// persist 把当前所有设置项写回 storage。
const persist = () => {
  setStorage(KEY, {
    dark: dark(),
    accent: accent(),
    fontSize: fontSize(),
    autoSave: autoSave(),
    notifications: notifications(),
    sound: sound(),
    quality: quality(),
    reminderDate: reminderDate(),
  });
}

const setDark = (v) => {
  darkP[1](!!v);
  setTheme(dark() ? "dark" : "light");
  persist();
}

const setAccent = (v) => {
  accentP[1](v);
  persist();
}

const setFontSize = (v) => {
  fontSizeP[1](clampFontSize(Number(v)));
  persist();
}

function setAutoSave(v) { autoSaveP[1](!!v); persist(); }
function setNotifications(v) { notifP[1](!!v); persist(); }
function setSound(v) { soundP[1](!!v); persist(); }
function setQuality(v) { qualityP[1](v); persist(); }
function setReminderDate(v) { dateP[1](v); persist(); }

// 启动即应用主题（上次存的）。
setTheme(dark() ? "dark" : "light");

export {
  dark, accent, fontSize, autoSave, notifications, sound, quality, reminderDate,
  setDark, setAccent, setFontSize, setAutoSave, setNotifications, setSound,
  setQuality, setReminderDate, DEFAULTS,
};
