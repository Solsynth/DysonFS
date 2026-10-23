import { createApp } from 'vue'
import { createPinia } from 'pinia'

import '@fontsource/ibm-plex-sans/latin-400.css'
import '@fontsource/ibm-plex-sans/latin-500.css'
import '@fontsource/ibm-plex-sans/latin-600.css'
import '@fontsource/ibm-plex-mono/latin-400.css'
import '@fontsource/ibm-plex-mono/latin-500.css'
import '@fontsource/ibm-plex-mono/latin-600.css'
import '../assets/main.css'

import PickerApp from './PickerApp.vue'
import { i18n } from '@/lib/i18n'
import { applyLocale, applyTheme, readLocale, readTheme } from '@/lib/prefs'

// picker.html resolves the theme before first paint; this keeps the stored
// preference and the language in step for a frame opened on its own.
applyTheme(readTheme())
applyLocale(readLocale())

const app = createApp(PickerApp)
app.use(createPinia())
app.use(i18n)
app.mount('#picker')
