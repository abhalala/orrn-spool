# TSPL/TSPL2 Programming Language - Comprehensive Context for LLM

## Document Overview

**Source:** TSC AUTO ID Technology Co., Ltd. - TSC BAR CODE PRINTER SERIES PROGRAMMING MANUAL
**Language:** TSPL/TSPL2 (Thermal Printer Programming Language)
**Purpose:** Programming manual for TSC barcode label printers

---

## 1. FUNDAMENTAL CONCEPTS

### 1.1 Document Conventions

| Convention | Description |
|------------|-------------|
| `[expression list]` | Items inside square brackets are optional, expression maximum length 2*1024 bytes |
| `<ESC>` | ASCII 27, control code of status polling command returns/runs the printer status immediately |
| `~` | ASCII 126, control code of status polling command returns the printer status only when the printer is ready |
| Space | ASCII 32, characters will be ignored in the command line |
| `"` | ASCII 34, beginning and ending of expression |
| CR, LF | ASCII 13, ASCII 10, denotes end of command line |
| NULL | ASCII 0, supported in the expression |

### 1.2 DPI Conversion

- **203 DPI:** 1 mm = 8 dots
- **300 DPI:** 1 mm = 12 dots

### 1.3 Printer Model Distinction

- **TSPL printers:** Basic programming language support, require MOVE command after KILL
- **TSPL2 printers:** Enhanced programming language with more features, no MOVE required after KILL

---

## 2. SETUP AND SYSTEM COMMANDS

### 2.1 SIZE - Define Label Size
```
SIZE m,n [UNIT]
SIZE width, height mm
SIZE width dot, height dot
```
- **m:** Label width (inch or mm)
- **n:** Label length (inch or mm)
- **UNIT:** Optional unit specification

### 2.2 GAP - Define Gap Distance
```
SIZE m,n
GAP m,n
```
- **m:** Gap distance between labels
- **n:** Gap offset (normally 0)

### 2.3 BLINE - Define Black Line Distance
```
SIZE m,n
BLINE m,n
```
- **m:** Black line thickness
- **n:** Black line offset

### 2.4 GAPDETECT / BLINEDETECT / AUTODETECT
```
GAPDETECT
BLINEDETECT
AUTODETECT
```
Automatic sensor calibration commands.

### 2.5 OFFSET - Define Print Offset
```
OFFSET m,n
```
- **m:** Horizontal offset (dots)
- **n:** Vertical offset (dots)

### 2.6 SPEED - Define Print Speed
```
SPEED n
```
Speed values vary by printer model (typically 1-6 for desktop, 1-12 for industrial).

### 2.7 DENSITY - Define Print Density
```
DENSITY n
```
- **n:** 0-15, controls darkness/heat applied

### 2.8 DIRECTION - Define Print Direction
```
DIRECTION n [,mirror]
```
- **n:** 0 or 1 (print direction)
- **mirror:** 0 = normal, 1 = mirror image

### 2.9 REFERENCE - Define Reference Point
```
REFERENCE x,y
```
Sets the reference point coordinates for label content.

### 2.10 SHIFT - Define Fine Adjustment
```
SHIFT m,n
```
- **m:** Horizontal shift (dots)
- **n:** Vertical shift (dots)

### 2.11 CLS - Clear Image Buffer
```
CLS
```
Clears all content in the image buffer.

### 2.12 FEED - Feed Label
```
FEED n
```
Feeds n dots of label.

### 2.13 BACKFEED / BACKUP
```
BACKFEED n
BACKUP n
```
Backs up n dots of label.

### 2.14 FORMFEED - Feed to Next Label
```
FORMFEED
```
Feeds to the next label position.

### 2.15 HOME - Position Label
```
HOME
```
Positions label at the home position.

### 2.16 PRINT - Print Label(s)
```
PRINT m[,n]
```
- **m:** Number of labels to print (use -1 for unlimited)
- **n:** Number of copies per label

### 2.17 SOUND - Generate Beep
```
SOUND n
```
Generates beep sound with frequency n.

### 2.18 CUT - Cut Label
```
CUT
```
Activates cutter mechanism.

### 2.19 LIMITFEED - Set Maximum Feed Length
```
LIMITFEED n
```
Sets maximum feed length for sensor calibration (n in dots).

### 2.20 SELFTEST - Printer Self Test
```
SELFTEST [option]
```
Options: None, PATTERN, WLAN, ETHERNET

---

## 3. LABEL FORMATTING COMMANDS

### 3.1 BAR - Draw Bar
```
BAR x,y,width,height
```
- **x,y:** Starting coordinates
- **width:** Bar width (dots)
- **height:** Bar height (dots)

### 3.2 BARCODE - Draw Barcode
```
BARCODE x,y,"code type",height,rotation, narrow,wide, alignment, "content"
```
**Parameters:**
- **x,y:** Starting coordinates
- **code type:** Barcode symbology (see table below)
- **height:** Barcode height (dots)
- **rotation:** 0=0°, 90=90°, 180=180°, 270=270°
- **narrow:** Narrow bar width
- **wide:** Wide bar width
- **alignment:** 0=left, 1=center, 2=right
- **content:** Barcode data

**Supported Barcode Types:**

| Code Type | Character Set | Max Length |
|-----------|---------------|------------|
| 128 | ASCII 0-127 | - |
| 128M | Manual code switching | - |
| EAN13 | 0-9 | 12 |
| EAN13+2 | 0-9 | 14 |
| EAN13+5 | 0-9 | 17 |
| EAN8 | 0-9 | 7 |
| UPCA | 0-9 | 11 |
| UPCE | 0-9 | 6 |
| 39 | 0-9, A-Z, special chars | - |
| 93 | ASCII 0-127 | - |
| CODABAR | 0-9, -$:/.+ | - |
| POSTNET | 0-9 | 5, 9, 11 |
| PDF417 | 2D barcode | - |
| QRCODE | 2D barcode | - |
| DMATRIX | 2D barcode | - |
| MAXICODE | 2D barcode | - |
| AZTEC | 2D barcode | - |
| TLC39 | TCIF Linked Code 39 | - |
| TELEPEN | ASCII 0-127 | 30 |
| TELEPENN | 0-9 | 60 |
| PLANET | 0-9 | 38 |
| CODE49 | ASCII 0-127 | 81 |
| MSI | 0-9 | - |
| PLESSEY | 0-9 | - |
| ITF14 | 0-9 | 13 |
| EAN14 | 0-9 | 13 |
| 11 | 0-9- | - |
| DPI | Deutsche Post Identcode | - |
| DPL | Deutsche Post Leitcode | - |
| LOGMARS | 0-9, A-Z, -.$/+% | - |

### 3.3 QRCODE - Draw QR Code
```
QRCODE x,y,level,cell width,rotation,mode, "content"
```
- **level:** Error correction level (L, M, Q, H)
- **cell width:** Module size (1-10)
- **rotation:** 0, 90, 180, 270
- **mode:** A=Auto, M=Manual

### 3.4 PDF417 - Draw PDF417
```
PDF417 x,y,width,height,rotation,[option], "content"
```
**Options:**
- **Pn:** Data compression (0=Auto, 1=Binary)
- **En:** Error correction level (0-8)
- **Mn:** Center pattern (0=Left, 1=Middle)
- **Wn:** Module width (2-9)
- **Hn:** Bar height (4-99)
- **Rn:** Maximum rows
- **Cn:** Maximum columns
- **Tn:** Truncation (0=No, 1=Yes)

### 3.5 DMATRIX - Draw DataMatrix
```
DMATRIX x,y,width,height,[c#,x#,r#,a#,row,col,] "content"
```
- **c#:** Escape sequence control character
- **x#:** Module size
- **r#:** Rotation
- **a#:** 0=Square, 1=Rectangle
- **row,col:** Symbol size

### 3.6 MAXICODE - Draw Maxicode
```
MAXICODE x,y,mode,[class,country,post,Lm,] "content"
```
- **mode:** 2=USA, 3=Canada, 4-5=Standard

### 3.7 AZTEC - Draw Aztec Code
```
AZTEC x,y,rotation,[size,ecp,flg,menu,multi,rev,] "content"
```
- **size:** Module size (1-20)
- **ecp:** Error control parameter

### 3.8 BITMAP - Draw Bitmap Image
```
BITMAP x,y,width,height,mode,bitmap data
```
- **mode:** 0=OVERWRITE, 1=OR, 2=XOR

### 3.9 BOX - Draw Rectangle
```
BOX x,y,x_end,y_end,line_thickness[,radius]
```
Draws a rectangle with optional rounded corners.

### 3.10 CIRCLE - Draw Circle
```
CIRCLE x,y,diameter,thickness
```

### 3.11 ELLIPSE - Draw Ellipse
```
ELLIPSE x,y,width,height,thickness
```

### 3.12 ERASE - Clear Region
```
ERASE x,y,x_width,y_height
```

### 3.13 REVERSE - Reverse Region
```
REVERSE x,y,x_width,y_height
```

### 3.14 DIAGONAL - Draw Diagonal Line
```
DIAGONAL x,y,width,height,thickness
```

### 3.15 TEXT - Draw Text
```
TEXT x,y,"font",rotation,x_multiplication,y_multiplication, "content"
```
**Built-in Fonts:**
- **"1":** 8x12 dots
- **"2":** 12x20 dots
- **"3":** 16x24 dots
- **"4":** 24x32 dots
- **"5":** 32x48 dots
- **"0":** or larger number for scalable font

### 3.16 BLOCK - Draw Text Block
```
BLOCK x,y,width,height,"font",rotation,x_mult,y_mult,alignment, "content"
```
- **alignment:** Line spacing in dots

### 3.17 PUTBMP - Place BMP Image
```
PUTBMP x,y,"filename" [,bpp][,contrast]
```
Supports 1-bit and 8-bit BMP files.

### 3.18 PUTPCX - Place PCX Image
```
PUTPCX x,y,"filename"
```

---

## 4. STATUS POLLING AND IMMEDIATE COMMANDS

### 4.1 <ESC>!? - Return Printer Status
Returns 4-byte status: `Status Byte #1, Status Byte #2, Status Byte #3, Status Byte #4`

**Status Byte #1 (Printer Status):**
- `@` (0x40): Normal
- `F`: Feeding
- `` ` ``: Pause mode
- `B`: Backing
- `E`: Printer error
- `K`: Waiting to press print key
- `L`: Waiting to take label
- `P`: Printing batch
- `W`: Imaging

**Status Byte #2 (Warning):**
- `@`: Normal
- `A`: Paper low
- `B`: Ribbon low
- `D`: Reversed
- `H`: Receive buffer full

**Status Byte #3 (Error):**
- `@`: Normal
- `A`: Print head overheat
- `B`: Stepping motor overheat
- `D`: Print head error
- `H`: Cutter jam
- `P`: Insufficient memory

**Status Byte #4 (Error):**
- `@`: Normal
- `A`: Paper empty
- `B`: Paper jam
- `D`: Ribbon empty
- `H`: Ribbon jam
- `` ` ``: Print head open

### 4.2 Other Immediate Commands

| Command | Description |
|---------|-------------|
| `<ESC>!C` | Check RTC presence |
| `<ESC>!D` | Enter dump mode |
| `<ESC>!O` | Return head open sensor status |
| `<ESC>!P` | Return paper status |
| `<ESC>!Q` | Return error status |
| `<ESC>!R` | Reset printer |
| `<ESC>!S` | Return 4-byte status |
| `<ESC>!F` | Feed one label |
| `<ESC>!.` | Cancel all printing files |
| `~!@` | Return mileage |
| `~!A` | Return free memory |
| `~!C` | Check RTC (old firmware) |
| `~!D` | Enter dump mode |
| `~!E` | Enable immediate commands |
| `~!F` | Return file list |
| `~!I` | Return codepage/country |
| `~!T` | Return printer model |

---

## 5. FILE MANAGEMENT COMMANDS

### 5.1 DOWNLOAD - Download File
```
DOWNLOAD [memory,] "FILENAME.BAS"
...program content...
EOP
```

**Memory Options:**
- Omitted: DRAM
- **F:** Flash memory
- **E:** Expansion memory

### 5.2 EOP - End of Program
```
EOP
```
Marks end of downloaded program.

### 5.3 FILES - List Files
```
FILES
```
Prints file list and memory information.

### 5.4 KILL - Delete File
```
KILL [memory,] "FILENAME"
KILL [memory,] "*"  // Delete all files
```

### 5.5 MOVE - Move Files to Flash
```
MOVE
```
Moves files from DRAM to Flash (TSPL only).

### 5.6 RUN - Execute Program
```
RUN "FILENAME.BAS"
```
Or simply call filename without .BAS extension.

---

## 6. BASIC COMMANDS AND FUNCTIONS

### 6.1 Variables and Data Types
- **String variables:** End with `$` (e.g., `A$`, `NAME$`)
- **Numeric variables:** No suffix (e.g., `A`, `COUNT`)
- **Global variables:** Prefixed with `@` (e.g., `@LABEL`, `@YEAR`)

### 6.2 Mathematical Functions
| Function | Description |
|----------|-------------|
| `ABS(n)` | Absolute value |
| `INT(n)` | Integer truncation |
| `VAL("string")` | Convert string to number |

### 6.3 String Functions
| Function | Description |
|----------|-------------|
| `ASC("char")` | ASCII value of character |
| `CHR$(n)` | Character from ASCII value |
| `LEFT$(string,n)` | Left n characters |
| `RIGHT$(string,n)` | Right n characters |
| `MID$(string,m,n)` | Middle characters |
| `LEN(string)` | String length |
| `STR$(n)` | Convert number to string |
| `STRCOMP(str1$,str2$[,mode])` | String comparison |
| `INSTR([start,]str1$,str2$)` | Find substring |
| `TRIM$(str$[,list$])` | Trim both ends |
| `LTRIM$(str$[,list$])` | Trim left |
| `RTRIM$(str$[,list$])` | Trim right |
| `XOR$(data$,password$)` | XOR encryption |

### 6.4 Date/Time Functions
| Function | Description |
|----------|-------------|
| `NOW$()` | Current date/time string |
| `NOW` | Days since 1900 |
| `FORMAT$(expression,style$)` | Format date/time/number |
| `DATEADD(interval,n,date)` | Add to date |

**FORMAT$ Predefined Styles:**
- "General Date", "Long Date", "Short Date"
- "Long Time", "Medium Time", "Short Time"
- "General Number", "Currency", "Fixed", "Standard", "Percent", "Scientific"

### 6.5 File I/O Functions
| Function | Description |
|----------|-------------|
| `OPEN "file",handle` | Open file |
| `CLOSE handle` | Close file |
| `READ handle,var1,var2,...` | Read from file |
| `WRITE handle,var1,var2,...` | Write to file |
| `SEEK handle,offset` | Move file pointer |
| `EOF(handle)` | Check end of file |
| `LOF("filename")` | File size |
| `LOC(handle)` | Current position |
| `FREAD$(handle,bytes)` | Read n bytes |
| `PUT handle,var$` | Append byte |
| `GET handle,var$` | Read byte |

### 6.6 Control Flow

**FOR...NEXT Loop:**
```
FOR var=start TO end [STEP increment]
    ...statements...
NEXT [var]
```

**WHILE...WEND Loop:**
```
WHILE condition
    ...statements...
WEND
```

**DO...LOOP:**
```
DO WHILE condition
    ...statements...
LOOP

DO UNTIL condition
    ...statements...
LOOP

DO
    ...statements...
LOOP WHILE condition

DO
    ...statements...
LOOP UNTIL condition
```

**IF...THEN...ELSE...ENDIF:**
```
IF condition THEN
    ...statements...
[ELSEIF condition THEN
    ...statements...]
[ELSE
    ...statements...]
ENDIF
```

**GOSUB...RETURN:**
```
GOSUB label
...
END

:label
...subroutine...
RETURN
```

**GOTO:**
```
GOTO label
...
:label
```

### 6.7 Input/Output Functions
| Function | Description |
|----------|-------------|
| `INPUT ["prompt",]var` | Input from keyboard |
| `OUT ["prompt",]var` | Output data |
| `OUTR ["prompt",]var` | Output via RS-232 |
| `INP$(n)` | Receive byte from COM port |
| `INP(n)` | Receive ASCII value |
| `LOB()` | Receiving buffer size |
| `GETKEY()` | Get key status (0=PAUSE, 1=FEED) |

### 6.8 Utility Functions
| Function | Description |
|----------|-------------|
| `BEEP` | Sound beep |
| `REM` | Comment |
| `END` | End program |
| `TEXTPIXEL(text$,font$,size)` | Text width in dots |
| `BARCODEPIXEL(content$,type$,narrow,wide)` | Barcode width in dots |
| `COPY file1$,file2$` | Copy file |
| `FSEARCH("pattern")` | Search files |
| `TOUCHPRESS()` | Touch screen status |
| `RECORDSET$("connection",query$)` | Database query |

---

## 7. DEVICE RECONFIGURATION COMMANDS

### 7.1 SET COUNTER
```
SET COUNTER @n length
@n = "initial_value"
```
Sets up a counter variable for serialization.

### 7.2 SET CUTTER
```
SET CUTTER ON/OFF/BATCH
```
- **ON:** Cut after each label
- **BATCH:** Cut after batch
- **OFF:** No cutting

### 7.3 SET PARTIAL_CUTTER
```
SET PARTIAL_CUTTER ON/OFF/BATCH
```

### 7.4 SET BACK
```
SET BACK n
SET BACK OFF
```
Backfeed distance after cut (dots).

### 7.5 SET KEYn
```
SET KEY1 ON/OFF
SET KEY2 ON/OFF
SET KEY3 ON/OFF
```
Enable/disable key functions.

### 7.6 SET LEDn
```
SET LED1 ON/OFF
SET LED2 ON/OFF
SET LED3 ON/OFF
```

### 7.7 SET PEEL
```
SET PEEL ON/OFF
```
Enable/disable peeling mode.

### 7.8 SET REWIND
```
SET REWIND ON/OFF/RS232
```

### 7.9 SET TEAR / SET STRIPER
```
SET TEAR ON/OFF      (TSPL2)
SET STRIPER ON/OFF   (TSPL)
```

### 7.10 SET GAP
```
SET GAP n/AUTO/OFF/0,/REVERSE/OBVERSE
```
Configure gap sensor sensitivity.

### 7.11 SET BLINE
```
SET BLINE REVERSE/OBVERSE
```

### 7.12 SET HEAD
```
SET HEAD ON/OFF
```
Enable/disable head open sensor.

### 7.13 SET RIBBON
```
SET RIBBON ON/OFF/INSIDE/OUTSIDE
```
- **ON:** Thermal transfer
- **OFF:** Direct thermal

### 7.14 SET COM1
```
SET COM1 baud,parity,data,stop
```
- **baud:** 24/48/96/19/38/57/115
- **parity:** N/E/O
- **data:** 7/8
- **stop:** 1/2

### 7.15 SET PRINTKEY
```
SET PRINTKEY ON/OFF/AUTO/num
```

### 7.16 SET REPRINT
```
SET REPRINT ON/OFF
```

### 7.17 GETSENSOR()
```
GETSENSOR(sensor$[,intension])
```
Sensor types: GAP, BLINE, RIBBON, PEEL, HEAD UP, HEAD TEMP, HEAD VOLT, BATTERY VOLT, BATTERY CAP

### 7.18 GETSETTING$()
```
GETSETTING$(app$,sec$,key$[,default$])
```
Returns printer configuration values.

---

## 8. PRINTER GLOBAL VARIABLES

### 8.1 Label Counter
```
@LABEL
```
Access current label count.

### 8.2 Date/Time Variables
| Variable | Description | Range |
|----------|-------------|-------|
| `YEAR` / `@YEAR` | Year | - |
| `MONTH` / `@MONTH` | Month | 01-12 |
| `DATE` / `@DATE` | Day | 01-31 |
| `WEEK` | Day of week | 1-7 |
| `HOUR` / `@HOUR` | Hour | 00-23 |
| `MINUTE` / `@MINUTE` | Minute | 00-59 |
| `SECOND` / `@SECOND` | Second | 00-59 |
| `@DAY` | Day of year | - |

### 8.3 System Information Variables
| Variable | Description |
|----------|-------------|
| `_MODEL$` | Printer model name |
| `_SERIAL$` | Printer serial number |
| `_VERSION$` | Firmware version |

---

## 9. NETWORK CONFIGURATION COMMANDS

### 9.1 Wi-Fi Module Commands
```
WLAN OFF                        // Disable Wi-Fi
WLAN SSID "ssid"                // Set SSID
WLAN WPA "key"                  // Set WPA key
WLAN WPA OFF                    // Disable WPA
WLAN WEP n,"key"                // Set WEP key
WLAN WEP OFF                    // Disable WEP
WLAN DHCP                       // Use DHCP
WLAN IP "ip","mask","gateway"   // Static IP
WLAN PORT n                     // Set port
```

### 9.2 Ethernet Commands
```
NET DHCP                        // Use DHCP
NET IP "ip","mask","gateway"    // Static IP
NET PORT n                      // Set port
NET NAME "name"                 // Set printer name
```

---

## 10. NFC SETTING COMMANDS

```
NFC FEATURE                     // Check NFC support
NFC STATUS                      // Check NFC status
NFC TIMEOUT m                   // Set timeout (0-3600 sec)
NFC READ                        // Read NFC tag
NFC WRITE "content"             // Write to NFC tag
NFC MODE OFF/READ/WRITE         // Set NFC mode
```

---

## 11. GPIO SETTING COMMANDS

### 11.1 SET GPO (Output)
```
SET GPOn signal,delay0,pulse0,delay1,pulse1,function
```
- **signal:** HIGH, LOW, POS, NEG
- **function:** FAULT, FAULT RIBBON, FAULT PAPER, FAULT CARRIAGE, PAUSE, TAKELABEL, IDLE, PRINT

### 11.2 SET GPI (Input)
```
SET GPIn signal,pulse,function
```
- **function:** PAUSE, PAUSE ON, PAUSE OFF, PRINT, PRINT n, CUT, FEED n, BACKFEED n, FORMFEED, INPUT n

---

## 12. SAMPLE PROGRAM STRUCTURE

### 12.1 Basic Label Program
```
SIZE 4,2              // 4" x 2" label
GAP 0.12,0            // 0.12" gap
DIRECTION 0           // Print direction
CLS                   // Clear buffer
TEXT 50,50,"3",0,1,1,"Hello World"
BARCODE 50,100,"128",80,1,0,2,2,"12345678"
PRINT 1               // Print 1 label
```

### 12.2 Downloaded Program with BASIC
```
DOWNLOAD "DEMO.BAS"
SIZE 4,2
GAP 0.12,0
DIRECTION 0
SET TEAR ON

COUNT=0
:START
COUNT=COUNT+1
CLS
TEXT 50,50,"3",0,1,1,"Label: "+STR$(COUNT)
BARCODE 50,100,"128",80,1,0,2,2,STR$(COUNT)
PRINT 1

IF COUNT<10 THEN
    GOTO START
ENDIF

END
EOP
RUN "DEMO.BAS"
```

### 12.3 Counter with Serialization
```
SET COUNTER @0 4
@0="0001"

SIZE 4,2
GAP 0.12,0
CLS
TEXT 50,50,"3",0,1,1,@0
BARCODE 50,100,"128",80,1,0,2,2,@0
PRINT 100
```

---

## 13. CODEPAGE SUPPORT

| Codepage | Description |
|----------|-------------|
| 437 | USA |
| 850 | Multilingual |
| 852 | Slavic |
| 860 | Portuguese |
| 863 | Canadian-French |
| 865 | Nordic |
| 866 | Russian |
| 1250 | Central European |
| 1252 | Latin 1 |
| 1253 | Greek |
| 1254 | Turkish |
| 1257 | Baltic |

---

## 14. IMPORTANT NOTES

1. **Maximum file count:**
   - DRAM: 50 files (TSPL/TSPL2)
   - Flash: 50 files (TSPL), 256 files (TSPL2)

2. **AUTO.BAS execution:**
   - Automatically runs on startup if present
   - Can be skipped by holding PAUSE+FEED during power-on

3. **Memory priority for AUTO.BAS:**
   - Before V6.80EZ: DRAM > FLASH > CARD
   - After V6.80EZ: DRAM > CARD > FLASH

4. **Printer models:**
   - Check printer model list for TSPL vs TSPL2 support
   - Desktop vs industrial have different speed/density ranges

---

## 15. FIRMWARE VERSION NOTES

- Commands marked "Since Vx.xx EZ" require minimum firmware version
- Some features only available in TSPL2 (enhanced) printers
- Alpha-2R series has special commands (SET PRINTQUALITY, SET STANDBYTIME)

---

*End of Comprehensive TSPL/TSPL2 Context Document*
