import { chromium } from 'playwright'
import { readFileSync } from 'node:fs'
const [cookieFile, hash, out, width] = process.argv.slice(2)
const [name, value] = readFileSync(cookieFile, 'utf8').trim().split(/=(.*)/s)
const browser = await chromium.launch()
const ctx = await browser.newContext({ viewport: { width: Number(width || 1280), height: 860 } })
await ctx.addCookies([{ name, value, url: 'http://localhost:8080', httpOnly: true, sameSite: 'Lax' }])
const page = await ctx.newPage()
page.on('pageerror', (e) => console.log('pageerror', e.message))
page.on('dialog', (d) => { console.log('dialog', d.message()); d.dismiss() })
await page.goto('http://localhost:8080/' + hash)
await page.waitForTimeout(4000)
if (process.env.CLICK) { await page.getByText(process.env.CLICK, { exact: true }).first().click(); await page.waitForTimeout(1500) }
await page.screenshot({ path: out })
await browser.close()
