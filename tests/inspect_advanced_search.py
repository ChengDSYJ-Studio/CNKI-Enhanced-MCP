"""Developer diagnostic for the current CNKI advanced-search DOM."""

from __future__ import annotations

import asyncio
import json

from playwright.async_api import async_playwright

from cnki_mcp.paths import configure_environment, ensure_runtime_dirs, profile_directory


async def run() -> None:
    configure_environment()
    ensure_runtime_dirs()
    async with async_playwright() as playwright:
        context = await playwright.chromium.launch_persistent_context(
            str(profile_directory("default")),
            headless=False,
            locale="zh-CN",
            viewport={"width": 1440, "height": 960},
            args=["--start-minimized"],
        )
        page = context.pages[0] if context.pages else await context.new_page()
        await page.goto("https://kns.cnki.net/kns8s/AdvSearch", wait_until="domcontentloaded", timeout=60_000)
        await page.wait_for_timeout(5_000)
        data = {
            "url": page.url,
            "title": await page.title(),
            "visible_inputs": await page.locator("input").evaluate_all(
                "els => els.filter(e => e.offsetParent !== null).map(e => ({id:e.id,name:e.name,class:e.className,placeholder:e.placeholder,type:e.type,value:e.value,parent:e.parentElement?.outerHTML.slice(0,2500)}))"
            ),
            "visible_selects": await page.locator("select").evaluate_all(
                "els => els.filter(e => e.offsetParent !== null).map(e => ({id:e.id,name:e.name,class:e.className,options:[...e.options].map(o => ({text:o.text,value:o.value})),parent:e.parentElement?.outerHTML.slice(0,2000)}))"
            ),
            "buttons": await page.locator("button, input[type=button], input[type=submit], a.btn-search").evaluate_all(
                "els => els.map(e => ({tag:e.tagName,id:e.id,class:e.className,text:(e.innerText||e.value||'').trim()}))"
            ),
            "condition_ancestors": await page.locator("input[type=text]").evaluate_all(
                "els => els.filter(e => e.offsetParent !== null && !e.placeholder).map(e => {let p=e; for(let i=0;i<4&&p;i++,p=p.parentElement){} return {input:e.outerHTML,ancestor:p?.outerHTML.slice(0,5000)}})"
            ),
            "row_controls": await page.locator("#gradetxt a, #gradetxt button, #gradetxt [class*='add'], #gradetxt [class*='del']").evaluate_all(
                "els => els.map(e => ({tag:e.tagName,class:e.className,text:(e.innerText||'').trim(),title:e.title,outer:e.outerHTML.slice(0,500)}))"
            ),
        }
        print(json.dumps(data, ensure_ascii=False, indent=2))
        await context.close()


if __name__ == "__main__":
    asyncio.run(run())
