package script

// MP3Template is local, embedded, and uses edited page bytes, never a download.
// Keeping it in Lua makes the same paging/annotation APIs available to users.
const MP3Template = `-- MPEG Layer III visual inspector (not an audio decoder).
assert(editor.set_view("hex"))
local _, pages = editor.page()
local bases, lengths, starts, total = {}, {}, {}, 0
for p=1,pages do
  bases[p], lengths[p] = editor.page_info(p)
  starts[p] = total
  total = total + lengths[p]
end
local cached, data = nil, nil
local function locate(off)
  local lo, hi = 1, pages
  while lo <= hi do
    local p = math.floor((lo+hi)/2)
    if off < starts[p] then hi=p-1
    elseif off >= starts[p]+lengths[p] then lo=p+1
    else return p, off-starts[p] end
  end
end
local function byte(off)
  local p, localoff = locate(off)
  if not p then return nil end
  if cached ~= p then
    data = assert(editor.read_bytes(p,0,lengths[p]))
    cached = p
  end
  return data:byte(localoff+1)
end
local function bits(value,shift,width)
  return math.floor(value/2^shift)%2^width
end
local annotations = {}
local function mark(off,bit,count,color,label,possible)
  local absolute = off*8+bit
  while count>0 do
    local p,localoff = locate(math.floor(absolute/8))
    if not p then return end
    local localbit = localoff*8+absolute%8
    local take = math.min(count,lengths[p]*8-localbit)
    annotations[#annotations+1] = {start=bases[p]*8+localbit,length=take,color=color,label=label,meaning=label,possible=possible or (count==1 and "0 = clear; 1 = set" or "Unsigned MSB-first field; see interpretation")}
    if #annotations>9900 then error("Template annotation limit exceeded") end
    absolute=absolute+take
    count=count-take
  end
end
local cursor=0
if total>=10 and byte(0)==73 and byte(1)==68 and byte(2)==51 then
  local version, flags = byte(3), byte(5)
  local a,b,c,d = byte(6),byte(7),byte(8),byte(9)
  assert(version>=2 and version<=4,"Unsupported ID3 version")
  assert(a<128 and b<128 and c<128 and d<128,"Invalid ID3 synchsafe size")
  local size=((a*128+b)*128+c)*128+d
  local footer=version==4 and bits(flags,4,1)==1 and 10 or 0
  assert(10+size+footer<=total,"Truncated ID3 tag")
  mark(0,0,24,"#55AAFF","ID3 tag signature")
  mark(3,0,8,"#77CC77","ID3v2 major version: "..version)
  mark(4,0,8,"#77CC77","ID3 revision: "..byte(4))
  mark(5,0,1,"#FFAA55","ID3 unsynchronisation: "..bits(flags,7,1))
  mark(5,1,1,"#DD88FF",version==2 and "ID3 compression flag" or "ID3 extended header flag")
  mark(5,2,1,"#55DDDD",version==2 and "Reserved (must be zero)" or "ID3 experimental flag")
  mark(5,3,1,"#99CC44",version==4 and "ID3 footer present flag" or "Reserved (must be zero)")
  mark(5,4,4,"#AAAAAA","Reserved ID3 flags (must be zero)")
  for i=6,9 do
    mark(i,0,1,"#AAAAAA","Synchsafe size: high bit must be zero")
    mark(i,1,7,"#DD77CC","Synchsafe size component; tag payload "..size.." bytes")
  end
  if size>0 then mark(10,0,size*8,"#8899AA","ID3 metadata payload (not decoded)") end
  if footer>0 then mark(10+size,0,80,"#DD77CC","ID3v2.4 footer") end
  cursor=10+size+footer
end
local br1={32,40,48,56,64,80,96,112,128,160,192,224,256,320}
local br2={8,16,24,32,40,48,56,64,80,96,112,128,144,160}
local rates={44100,48000,32000}
local versions={[0]="2.5",[2]="2",[3]="1"}
local channels={[0]="stereo",[1]="joint stereo",[2]="dual channel",[3]="mono"}
local emphasis={[0]="none",[1]="50/15 microseconds",[3]="CCIT J.17"}
local function header(off)
  if off+4>total then return nil end
  local a,b,c,d=byte(off),byte(off+1),byte(off+2),byte(off+3)
  local v,layer=bits(b,3,2),bits(b,1,2)
  local bi,si=bits(c,4,4),bits(c,2,2)
  if a~=255 or b<224 or v==1 or layer~=1 or bi==0 or bi==15 or si==3 or bits(d,0,2)==2 then return nil end
  local rate=rates[si+1]/(v==3 and 1 or v==2 and 2 or 4)
  local bitrate=(v==3 and br1[bi] or br2[bi])*1000
  local size=math.floor((v==3 and 144 or 72)*bitrate/rate)+bits(c,1,1)
  return {v=v,b=b,c=c,d=d,rate=rate,bitrate=bitrate,size=size}
end
local frames,first,last=0,nil,nil
-- Bounded inspection: no whole-file string allocation, at most 400 frames
-- and 8 MiB of resynchronisation scanning. Re-run an adapted template to
-- inspect larger streams; do not silently claim to have decoded all frames.
local scanned=0
while cursor+4<=total and frames<400 and scanned<8*1024*1024 do
  local h=header(cursor)
  local following=h and cursor+h.size or 0
  local plausible=h and following<=total
  if plausible and following+4<=total then
    -- Require another header at the calculated boundary, except ID3v1 tail.
    plausible=header(following)~=nil or (total-following==128 and byte(following)==84 and byte(following+1)==65 and byte(following+2)==71)
  end
  if plausible then
    frames=frames+1
    first=first or cursor
    last=following
    local prefix="Frame "..frames.." ["..cursor..","..following..") "
    mark(cursor,0,h.size*8,"#778899",prefix..h.size.." bytes; end is exclusive")
    mark(cursor,0,11,"#FF6655",prefix.."sync: eleven one bits","2047 = sync; all other values invalid")
    mark(cursor,11,2,"#55BBFF","MPEG version "..versions[h.v],"00 = MPEG 2.5; 01 = reserved; 10 = MPEG 2; 11 = MPEG 1")
    mark(cursor,13,2,"#55DD88","Layer III (01)","00 = reserved; 01 = Layer III; 10 = Layer II; 11 = Layer I")
    local crc=bits(h.b,0,1)==0
    mark(cursor,15,1,"#FFCC44",crc and "Protection=0: 16-bit CRC follows header" or "Protection=1: no CRC","0 = CRC follows; 1 = no CRC")
    mark(cursor,16,4,"#DD88FF","Bitrate: "..h.bitrate.." bits/s",h.v==3 and "0 = free format; indices 1–14 (kbps): 32,40,48,56,64,80,96,112,128,160,192,224,256,320; 15 = invalid" or "0 = free format; indices 1–14 (kbps): 8,16,24,32,40,48,56,64,80,96,112,128,144,160; 15 = invalid")
    mark(cursor,20,2,"#55DDDD","Sample rate: "..h.rate.." Hz","Indices 0,1,2: 44100,48000,32000 Hz divided by 1 (MPEG 1), 2 (MPEG 2), 4 (MPEG 2.5); 3 = reserved")
    mark(cursor,22,1,"#FFAA77","Padding: "..bits(h.c,1,1).." extra byte")
    mark(cursor,23,1,"#AAAAAA","Private bit: application-defined")
    local mode=bits(h.d,6,2)
    mark(cursor,24,2,"#99CC44","Channel mode: "..channels[mode],"00 = stereo; 01 = joint stereo; 10 = dual channel; 11 = mono")
    mark(cursor,26,1,"#CC9944",mode==1 and ("Joint stereo: MS stereo="..bits(h.d,5,1)) or "Mode extension: unused outside joint stereo")
    mark(cursor,27,1,"#CCAA44",mode==1 and ("Joint stereo: intensity stereo="..bits(h.d,4,1)) or "Mode extension: unused outside joint stereo")
    mark(cursor,28,1,"#BB88AA","Copyright flag: "..bits(h.d,3,1))
    mark(cursor,29,1,"#88AABB","Original media flag: "..bits(h.d,2,1))
    mark(cursor,30,2,"#CCAA88","Emphasis: "..emphasis[bits(h.d,0,2)],"00 = none; 01 = 50/15 microseconds; 10 = reserved; 11 = CCIT J.17")
    if crc then mark(cursor+4,0,16,"#FFCC44","CRC-16 check word (not verified)") end
    cursor=following
  else
    cursor=cursor+1
    scanned=scanned+1
  end
end
assert(frames>0,"No complete supported MPEG Layer III frames found; free-format and reserved headers are not supported")
-- Parse first; preserve existing annotations if parsing fails or is canceled.
-- Publish definitions once, rather than redrawing after every field.
assert(editor.set_marks(annotations))
assert(editor.goto_offset(first))
ui.notify("MP3: "..frames.." complete frames annotated, bytes ["..first..","..last.."). Split panel shows live values, meanings and possible values. Inspection stops at 400 frames or 8 MiB resync scan; not an audio decoder.")
return frames
`
