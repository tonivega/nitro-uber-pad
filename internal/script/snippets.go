package script

// Snippets contains advanced scripting examples shown in the help.
const Snippets = `ADVANCED SCRIPTING SNIPPETS
===========================
Paste a snippet into Settings > Actions & Shortcuts > Add action. Give it a
label, a menu, an optional shortcut (e.g. Mod+Shift+R) and tick "Show in
toolbar" to get a button. Text in this window can be selected and copied.

-- 1. Button / shortcut that runs remote code from an external HTTP server --
-- The built-in "Remote Action" (Tools menu, Mod+Shift+R, toolbar) is exactly
-- this (Settings > Actions > Reset to defaults adds it to older configs).
-- Set the URL in Settings > Variables: remote_action_url
local url = config.get("remote_action_url") or "http://127.0.0.1:8000/nup-action.lua"
local code, status, err = http.get(url, 5)            -- 5 second timeout
if not code then ui.alert("Download failed: " .. err) return end
if status ~= 200 then ui.alert("HTTP " .. status .. " from " .. url) return end
local ok, res = nup.eval(code, "remote_action")       -- like eval() in PHP
if not ok then ui.alert("Remote code failed: " .. tostring(res)) end

-- nup-action.lua, served by any HTTP server. It is downloaded every time the
-- button is pressed, so its behaviour can change without touching the editor:
local lines = select(2, editor.text():gsub("\n", "\n")) + 1
editor.insert("[remote stamp " .. nup.date("2006-01-02 15:04") .. "]")
ui.notify("Remote script ran on " .. editor.title() .. " (" .. lines .. " lines)")

-- Serve it for testing (in the folder containing nup-action.lua):
--   python3 -m http.server 8000
-- SECURITY: remote code runs with the full scripting API (files, HTTP...).
-- Only load code from servers you trust, preferably over https.

-- 2. A whole remote toolbox: the server returns a table of functions -------
-- tools.lua on the server:
--   return {
--     upper = function() editor.set_text(editor.text():upper()) end,
--     stamp = function() editor.insert(nup.date("15:04:05")) end,
--   }
local code, status, err = http.get(config.get("tools_url") or "http://127.0.0.1:8000/tools.lua")
if status ~= 200 then ui.alert("tools: " .. tostring(err or status)) return end
local ok, tools = nup.eval(code, "tools")
if not ok or type(tools) ~= "table" then ui.alert("tools: " .. tostring(tools)) return end
ui.prompt("Remote tools", "Tool name:", function(name)
  if name == nil then return end
  if tools[name] then tools[name]() else ui.alert("Unknown tool: " .. name) end
end)

-- 3. Macros stored in settings variables (dynamic scripting) --------------
-- Settings > Variables: macro_1 = editor.insert("Hello " .. nup.date())
local ok, err = nup.eval(config.get("macro_1") or "", "macro_1")
if not ok then ui.alert(err) end

-- 4. Calculator: evaluate the expression on the cursor line ----------------
local line_no = editor.cursor()
local n, expr = 0, ""
for l in (editor.text() .. "\n"):gmatch("(.-)\n") do
  n = n + 1
  if n == line_no then expr = l break end
end
local ok, v = nup.eval("return " .. expr, "calc")
if ok then
  editor.set_cursor(line_no, nup.ulen(expr) + 1)
  editor.insert(" = " .. tostring(v))
else
  ui.alert(v)
end

-- 5. GET with status code, headers and the default timeout -----------------
local body, status, err, headers = http.get("https://example.com/")
if not body then ui.alert("Network error: " .. err) return end
ui.notify("HTTP " .. status .. ", " .. #body .. " bytes, type " ..
  tostring(headers["content-type"]) .. ", timeout " .. http.timeout() .. " s")

-- 6. POST the current page and show the answer in a new tab ----------------
local body, status, err = http.post("https://httpbin.org/post", editor.text(),
  "text/plain; charset=utf-8", 15)
if not body then ui.alert(err) return end
ui.new_tab()
editor.set_text("HTTP " .. status .. "\n\n" .. body)

-- 7. JSON API call with custom method, headers and token -------------------
local body, status, err = http.request{
  method = "PUT",
  url = "https://api.example.com/notes/" .. http.urlencode(editor.title()),
  body = '{"title":"' .. editor.title() .. '","size":' .. editor.size() .. '}',
  headers = { ["Content-Type"] = "application/json",
              ["Authorization"] = "Bearer " .. (config.get("api_token") or "") },
  timeout = 10,
}
if status < 200 or status > 299 then
  ui.alert("API: " .. tostring(err or status) .. "\n" .. tostring(body))
end

-- 8. Webhook (chat notification) with a form-encoded body ------------------
local _, status, err = http.post(config.get("webhook_url"),
  "text=" .. http.urlencode("Saved " .. (editor.path() or editor.title())),
  "application/x-www-form-urlencoded")
ui.notify("webhook: " .. tostring(err or status))

-- 9. Positioning the cursor -----------------------------------------------
-- go to the end of the page
local last = select(2, editor.text():gsub("\n", "\n")) + 1
editor.set_cursor(last, 1e9)                          -- columns are clamped
-- find text on the page and put the cursor on it
ui.prompt("Find", "Text:", function(q)
  if not q or q == "" then return end
  local n = 0
  for l in (editor.text() .. "\n"):gmatch("(.-)\n") do
    n = n + 1
    local s = l:find(q, 1, true)
    if s then editor.set_cursor(n, nup.ulen(l:sub(1, s - 1)) + 1) return end
  end
  ui.notify("Not found: " .. q)
end)
-- hex view (non-text pages): line = 16-byte row, col = byte in the row
local off = 0x40
editor.set_cursor(math.floor(off / 16) + 1, off % 16 + 1)

-- 10. Files changed on disk by other programs -----------------------------
if editor.disk_changed() then
  ui.confirm("Changed on disk", "Reload " .. editor.title() .. " from disk?", function(yes)
    if yes then editor.reload() end
  end)
end
-- save without the "changed on disk" confirmation
local ok, err = editor.save(true)
`
