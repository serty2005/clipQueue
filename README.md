# ClipQueue

Windows-only application for managing clipboard history with global hotkeys.

## Features

- **Queue Mode**: Press `Alt+C` to toggle queue mode
- **Paste Next**: Press `Alt+V` to paste the next item from the queue
- **Clipboard Watcher**: Automatically detects clipboard changes

## Building

```bash
go build -o clipqueue.exe
```

## Running

```bash
./clipqueue.exe
```

## Testing

### General Testing

1. Run the application:
   ```bash
   ./clipqueue.exe
   ```

2. Check the console output for "Host started" message.

3. Press `Alt+C` - you should see "QUEUE ON/OFF" in the console.

4. Press `Alt+V` - you should see "PASTE NEXT" in the console.

5. Copy any text in another application - you should see detailed clipboard information in the console, including type, length, and preview.

6. Copy one or more files in File Explorer - you should see the number of files and a preview of their paths in the console.

7. Copy an image (e.g., from Paint or a browser) - you should see the image type and preview information in the console.

8. Press `Ctrl+C` to stop the application - you should see "Host stopping" and then "ClipQueue stopped" in the console.

### Testing CF_HDROP Write Functionality

To test writing files to the clipboard (CF_HDROP format):

1. First, you need to create a small test program or add temporary code to the main application to call the `Write()` function with file paths.

2. Example temporary code you can add to `main.go`:
   ```go
   package main

   import (
       "github.com/serty2005/clipqueue/internal/logger"
       "github.com/serty2005/clipqueue/platform/windows"
   )

   func main() {
       logger.Init()
       
       // Test writing files to clipboard
       filesToCopy := []string{
           "C:\\Path\\To\\Your\\File1.txt",
           "C:\\Path\\To\\Your\\File2.jpg",
       }
       
       err := windows.Write(windows.ClipboardContent{
           Type:  windows.Files,
           Files: filesToCopy,
       })
       
       if err != nil {
           logger.Error("Failed to write files to clipboard: %v", err)
       } else {
           logger.Info("Successfully wrote %d files to clipboard", len(filesToCopy))
       }
       
       // Wait for user input
       logger.Info("Press Enter to exit...")
       var input string
       fmt.Scanln(&input)
   }
   ```

3. Build and run this test program.

4. After running, open File Explorer and press `Ctrl+V`. You should see the files you specified in the clipboard appear as copied files.

## What We Don't Support Yet

- RTF (Rich Text Format)
- HTML format
- BITMAPV5HEADER with advanced features (like BI_BITFIELDS)
- Compressed DIB formats (e.g., BI_RLE8, BI_RLE4)
- Other specialized clipboard formats

## Configuration

The application will create a `config.yml` file in the same directory as the executable with the following default settings:

```yaml
app:
  data_dir: C:\Users\YourUsername\AppData\Roaming\ClipQueue
hotkeys:
  toggle_queue: Alt+C
  paste_next: Alt+V
clipboard:
  watch_debounce_ms: 30
queue:
  default_order: LIFO
```

## Logs

Logs are stored in the `data_dir/logs/app.log` file.
