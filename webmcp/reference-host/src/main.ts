import { mount } from 'svelte'
import App from './App.svelte'
import './style.css'

const target = document.getElementById('app')
if (!target) throw new Error('Mount target is missing')

const app = mount(App, { target })

export default app
