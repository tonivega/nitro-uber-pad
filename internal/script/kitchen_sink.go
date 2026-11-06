package script

// KitchenSink is embedded so release binaries do not depend on a checkout.
const KitchenSink = `-- Nitro UberPad kitchen sink. Load via Tools, then Run Lua (Mod+Enter).
-- Trusted local demo: creates a unique directory beneath your home, writes
-- files there and starts short local programs. No network access by default.
local root = nup.join(nup.home(), "UberPad-kitchen-" .. nup.date("20060102-150405"))
assert(nup.mkdir(root))
local path = nup.join(root, "roundtrip.bin")
local value = "Hello Lua!\nUnicode: café 😀\nBinary: " .. string.char(0, 255)
assert(not nup.exists(path), "Demo directory already exists; wait a second and retry")
assert(fs.write(path, value))
local loaded, err = fs.read(path)
assert(loaded, err)
assert(loaded == value, "binary file roundtrip failed")
local missing, why = fs.read(nup.join(root, "missing.txt"))
assert(missing == nil and why)
print("File roundtrip OK", path, #loaded, nup.os, nup.version)

local ok, answer = nup.eval("return 6 * 7", "kitchen eval")
assert(ok and answer == 42)
config.set("kitchen_last_dir", root)
assert(config.get("kitchen_last_dir") == root)

-- Explicit argument arrays: no shell expansion or quoting required.
local program, args, slow
if nup.os == "windows" then
  program = "cmd.exe"
  args = {"/C", "echo %NUP_KITCHEN%"}
  slow = {"/C", "ping -n 4 127.0.0.1 >nul"}
else
  program = "/bin/sh"
  args = {"-c", 'printf "%s\\n" "$NUP_KITCHEN"; printf "%s\\n" "$1"', "demo", "one argument with spaces"}
  slow = {"-c", "sleep 2; printf done"}
end
local result = assert(process.run{
  program=program, args=args, env={NUP_KITCHEN="environment works"},
  cwd=root, timeout=5,
})
assert(result.exit_code == 0, result.error)
print("Foreground:", result.output)
local bg = assert(process.run{program=program,args=slow,timeout=0,background=true})
local timed = assert(process.run{program=program,args=slow,timeout=0.05,background=true})
print("Background process",bg,"and timeout test",timed)

local child = task.run('assert(task.sleep(0.2)); print("Background Lua finished")', "Kitchen background", 3)
local canceled = task.run('while true do end', "Canceled before execution")
assert(task.cancel(canceled))
local timeout = task.run('while true do end', "Lua timeout test", 0.05)
print("Lua tasks",child,timeout)

local once = timer.after(0.3, function() print("One-shot callback") end)
local ticks, repeating = 0, nil
repeating = timer.every(0.5, function()
  ticks = ticks + 1
  print("Repeat",ticks)
  if ticks == 3 then assert(timer.cancel(repeating)) end
end)
timer.at(os.time()+2, 'print("Wall-clock callback")')
local canceledTimer = timer.after(60, 'error("Canceled timer ran")')
assert(timer.cancel(canceledTimer))
-- Intentional error: verify failed timer status and activity logs.
timer.after(0.4, 'error("Kitchen sink intentional timer failure")')

assert(ui.action{
  id="kitchen_tasks", label="Kitchen tasks", menu="Kitchen sink", toolbar=true,
  script='for _, t in ipairs(task.list()) do print(t.id, t.kind, t.state, t.name) end',
})
assert(ui.action{id="kitchen_mp3_template",label="Apply MP3 bit template to current document",menu="Kitchen sink",toolbar=true,
  script='local ok, why = editor.apply_template("mp3"); if not ok then ui.alert(tostring(why)) end'})
assert(ui.action{id="kitchen_remove",label="Remove kitchen controls",menu="Kitchen sink",
  script='ui.remove_action("kitchen_tasks"); ui.remove_action("kitchen_mp3_template"); ui.remove_action("kitchen_remove")'})

-- Path-aware editor APIs on a separate text file; the demo source is retained.
local note = nup.join(root,"note.txt")
assert(fs.write(note,"Kitchen sink note\n"))
assert(editor.open(note))
editor.insert("Created by Lua\n")
assert(editor.save())
for _, tab in ipairs(ui.tabs()) do print("Tab",tab.index,tab.path or "unsaved",tab.modified) end
assert(ui.select_tab(note))
ui.prompt("Kitchen callback", "Optional note (Cancel is safe):", function(text)
  if text then assert(fs.write(nup.join(root,"prompt.txt"),text)) end
end)
ui.confirm("Kitchen callback", "Show a notification?", function(yes)
  if yes then ui.notify("Dialog callback worked") end
end)

-- Optional network test (opt in in Settings > Variables: kitchen_http_url).
local url = config.get("kitchen_http_url")
if url and url ~= "" then
  local body,status,why,headers = http.get(url,2)
  print("HTTP",status,why or "OK",body and #body or 0)
end
-- Optional real MP3 inspection. Opt in with kitchen_mp3_url in Settings:
-- https://www.soundhelix.com/examples/mp3/SoundHelix-Song-1.mp3
-- The sample is downloaded as DATA into an unsaved, paged tab. It is not
-- executed or written to a permanent local path; use Save As if desired.
-- Network failures are reported without invalidating the other demo tests.
local mp3url = config.get("kitchen_mp3_url")
if mp3url and mp3url ~= "" then
  local opened, why = editor.open_url(mp3url)
  if not opened then
    ui.notify("MP3 download failed: " .. tostring(why))
  else
    assert(editor.set_view("hex"))
    -- A reusable visual template. Reads edited pages explicitly, avoiding
    -- assumptions that MPEG frames or ID3 headers align to editor pages.
    local _, pages = editor.page()
    local chunks, bases, lengths = {}, {}, {}
    for p = 1, pages do
      local base, size = editor.page_info(p)
      bases[p], lengths[p] = base, size
      chunks[p] = assert(editor.read_bytes(p, 0, size))
    end
    local bytes = table.concat(chunks)
    local function original_bit(byte, bit)
      local pos = byte
      for p = 1, pages do
        if pos < lengths[p] then return (bases[p] + pos) * 8 + bit end
        pos = pos - lengths[p]
      end
      return nil
    end
    -- Split marks at page boundaries, so this template also works with pages
    -- whose edited lengths differ from their original lengths.
    local annotations = {}
    local function mark(byte, bit, length, color, label)
      if length <= 0 then return end
      local absolute = byte * 8 + bit
      local left = length
      local consumed = 0
      for p = 1, pages do
        local span = lengths[p] * 8
        if absolute < consumed + span and left > 0 then
          local localbit = math.max(0, absolute - consumed)
          local take = math.min(left, span - localbit)
          annotations[#annotations+1] = {start=bases[p]*8 + localbit, length=take, color=color, label=label}
          left = left - take
          absolute = absolute + take
        end
        consumed = consumed + span
      end
    end
    local cursor = 0 -- concatenated-buffer byte offset
    if bytes:sub(1,3) == "ID3" and #bytes >= 10 then
      mark(0,0,24,"#55AAFF","ID3 signature")
      mark(3,0,16,"#77CC77","ID3 version")
      mark(5,0,8,"#FFAA55","ID3 flags")
      mark(6,0,32,"#DD77CC","ID3 synchsafe payload size")
      local a,b,c,d = bytes:byte(7,10)
      if a<128 and b<128 and c<128 and d<128 then
        local size = ((a*128+b)*128+c)*128+d
        mark(10,0,math.min(size,#bytes-10)*8,"#8899AA","ID3 payload")
        cursor = math.min(10+size,#bytes)
      end
    end
    -- Inspect MPEG audio frame headers (including bit-level fields), and
    -- follow known-length Layer III frames. This is a visual inspector, not
    -- an audio decoder; reserved/free-format headers are skipped safely.
    local br1 = {32,40,48,56,64,80,96,112,128,160,192,224,256,320}
    local br2 = {8,16,24,32,40,48,56,64,80,96,112,128,144,160}
    local rates = {44100,48000,32000}
    local frames, first = 0, nil
    while cursor+4 <= #bytes and frames < 100 do
      local a,b,c,d = bytes:byte(cursor+1,cursor+4)
      local version = math.floor(b/8)%4
      local layer = math.floor(b/2)%4
      local bi, si = math.floor(c/16), math.floor(c/4)%4
      if a==255 and b>=224 and version~=1 and layer==1 and bi>0 and bi<15 and si<3 then
        first = first or cursor
        local rate = rates[si+1]
        if version==2 then rate=rate/2 elseif version==0 then rate=rate/4 end
        local bitrate = (version==3 and br1[bi] or br2[bi])*1000
        local frameSize = math.floor((version==3 and 144 or 72)*bitrate/rate)+math.floor(c/2)%2
        mark(cursor,0,11,"#FF6655","MPEG sync (11 bits)")
        mark(cursor,11,2,"#55BBFF","MPEG version " .. version)
        mark(cursor,13,2,"#55DD88","Layer III")
        mark(cursor,15,1,"#FFCC44","CRC protection bit")
        mark(cursor,16,4,"#DD88FF","bitrate " .. bitrate .. " bps")
        mark(cursor,20,2,"#55DDDD","sample rate " .. rate .. " Hz")
        mark(cursor,22,1,"#FFAA77","padding")
        mark(cursor,23,1,"#AAAAAA","private")
        mark(cursor,24,2,"#99CC44","channel mode")
        mark(cursor,26,2,"#CC9944","mode extension")
        mark(cursor,28,1,"#BB88AA","copyright")
        mark(cursor,29,1,"#88AABB","original")
        mark(cursor,30,2,"#CCAA88","emphasis")
        frames = frames+1
        cursor = cursor+math.max(frameSize,4)
      else cursor=cursor+1 end
    end
    -- One atomic publication; navigation retains these tab-local definitions.
    assert(editor.set_marks(annotations))
    if first then assert(editor.goto_offset(first)) end
    print("MP3 visual template:",frames,"Layer III headers annotated; MSB-first bit labels and colors.")
    assert(ui.action{id="kitchen_mp3",label="Inspect MP3 header page",menu="Kitchen sink",
      script='editor.set_view("hex"); ui.notify("Use Ctrl+G and a byte offset; annotations follow original file coordinates.")'})
  end
end
ui.notify("Kitchen sink queued. Open Task Runner, Refresh, inspect output/errors, and try Cancel. Files: " .. root)
`
