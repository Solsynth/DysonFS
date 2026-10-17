import { createApp } from 'vue'
import { createPinia } from 'pinia'

import '@fontsource/ibm-plex-sans/latin-400.css'
import '@fontsource/ibm-plex-sans/latin-500.css'
import '@fontsource/ibm-plex-sans/latin-600.css'
import '@fontsource/ibm-plex-mono/latin-400.css'
import '@fontsource/ibm-plex-mono/latin-500.css'
import '@fontsource/ibm-plex-mono/latin-600.css'
import './assets/main.css'

import App from './App.vue'
import router from './router'
import { applyLocale, applyTheme, readLocale, readTheme } from './lib/prefs'
import { i18n } from './lib/i18n'

applyTheme(readTheme())
applyLocale(readLocale())

const app = createApp(App)

app.use(createPinia())
app.use(i18n)
app.use(router)

app.mount('#app')
