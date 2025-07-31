package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-vgo/robotgo"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text"
	"github.com/mb-14/gomarkov"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
)

const (
	screenWidth  = 460  // Outer ring radius (200) * 2 + padding for characters
	screenHeight = 460
	trainingDataFile = "markov_training.json"
	rawTextFile = "typed_text.txt"
)

type Game struct {
	gamepadIDsBuf  []ebiten.GamepadID
	gamepadIDs     map[ebiten.GamepadID]struct{}
	axes           map[ebiten.GamepadID][]string
	pressedButtons map[ebiten.GamepadID][]string

	// Ring keyboard state
	rings          [2][2][]string // 2 rings, 2 sets (main/secondary)
	currentSet     int            // 0 for main set, 1 for secondary set
	selectedRing   int            // Which ring is active (0 or 1)
	selectedIndex  int
	joystickAngle  float64
	lastButtonTime time.Time
	font           font.Face
	uppercase      bool // Toggle between uppercase and lowercase
	
	// Visibility state
	lastInputTime time.Time
	opacity       float64
	isVisible     bool
	
	// Window position
	windowX float64
	windowY float64
	windowInitialized bool
	
	// Markov chain for word prediction
	markovChain    *gomarkov.Chain
	currentSentence []string
	recentWords     []string  // Track recent words for training
	trainingData    [][]string // All training sentences
	nextPrediction  string     // Current word prediction to display
	wordFrequency   map[string]int // Track word frequencies for autocomplete
}

// saveTrainingData saves all training sentences to a file
func (g *Game) saveTrainingData() error {
	// Get user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	
	configDir := filepath.Join(homeDir, ".config", "control")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}
	
	filePath := filepath.Join(configDir, trainingDataFile)
	data, err := json.Marshal(g.trainingData)
	if err != nil {
		return err
	}
	
	return os.WriteFile(filePath, data, 0644)
}

// loadTrainingData loads training sentences from file
func (g *Game) loadTrainingData() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	
	filePath := filepath.Join(homeDir, ".config", "control", trainingDataFile)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, that's ok
			g.trainingData = [][]string{}
			return nil
		}
		return err
	}
	
	return json.Unmarshal(data, &g.trainingData)
}

// appendToRawText appends text to the raw text file
func (g *Game) appendToRawText(text string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	
	configDir := filepath.Join(homeDir, ".config", "control")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}
	
	filePath := filepath.Join(configDir, rawTextFile)
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	
	_, err = f.WriteString(text)
	return err
}

// loadRawTextAndTrain loads all previously typed text and trains the Markov chain
func (g *Game) loadRawTextAndTrain() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	
	filePath := filepath.Join(homeDir, ".config", "control", rawTextFile)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, that's ok
			return nil
		}
		return err
	}
	
	// Parse the text into sentences and words
	text := string(data)
	if text == "" {
		return nil
	}
	
	// Split by common sentence endings
	sentences := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})
	
	// Process each sentence
	for _, sentence := range sentences {
		// Clean and split into words
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}
		
		// Split into words
		words := strings.Fields(sentence)
		if len(words) > 1 {
			// Clean each word (remove punctuation except apostrophes)
			cleanWords := make([]string, 0, len(words))
			for _, word := range words {
				// Remove trailing punctuation
				word = strings.TrimRight(word, ",.;:\"'!?")
				// Remove leading punctuation
				word = strings.TrimLeft(word, "\"'")
				if word != "" {
					cleanWords = append(cleanWords, strings.ToLower(word))
				}
			}
			
			if len(cleanWords) > 1 {
				g.markovChain.Add(cleanWords)
			}
			
			// Add words to frequency map
			for _, word := range cleanWords {
				g.wordFrequency[word]++
			}
		}
	}
	
	log.Printf("Loaded and trained on raw text file (%d bytes)", len(data))
	return nil
}

// updatePrediction generates the next word prediction based on current context
func (g *Game) updatePrediction() {
	if g.markovChain == nil {
		g.nextPrediction = ""
		log.Printf("No prediction: markovChain is nil")
		return
	}
	
	// If no sentence started yet, try to predict from empty context
	if len(g.currentSentence) == 0 {
		// Try to generate a starting word
		next, err := g.markovChain.Generate([]string{""})
		if err == nil && next != "" {
			g.nextPrediction = next
			log.Printf("Initial prediction: '%s'", next)
		} else {
			g.nextPrediction = ""
			log.Printf("No initial prediction available")
		}
		return
	}
	
	// Check if we have a partial word being typed
	currentWord := g.currentSentence[len(g.currentSentence)-1]
	
	// Use the appropriate context
	var contextWord string
	var isPartialWord bool
	
	
	if currentWord == "" && len(g.currentSentence) > 1 {
		// Just typed space, use previous complete word
		contextWord = g.currentSentence[len(g.currentSentence)-2]
		isPartialWord = false
	} else if currentWord != "" {
		// We have a partial or complete word
		if len(g.currentSentence) > 1 {
			// Use previous word as context for prediction
			contextWord = g.currentSentence[len(g.currentSentence)-2]
		} else {
			// First word, try to predict based on partial
			contextWord = currentWord
		}
		isPartialWord = true
	}
	
	if contextWord != "" || isPartialWord {
		if !isPartialWord || len(g.currentSentence) > 1 {
			// Generate next word prediction based on previous word
			next, err := g.markovChain.Generate([]string{contextWord})
			if err == nil && next != "" {
				g.nextPrediction = next
				log.Printf("Prediction updated: context='%s' -> prediction='%s'", contextWord, next)
			} else {
				// If the exact word isn't known, try common follow-ups
				log.Printf("No prediction for '%s', trying fallbacks", contextWord)
				
				// Try to find any word that commonly follows short words
				if len(contextWord) <= 3 {
					// For short words, try common patterns
					commonFollowUps := []string{"the", "a", "is", "are", "and", "to", "in", "it", "that", "of"}
					if len(commonFollowUps) > 0 {
						// Pick a common word
						g.nextPrediction = commonFollowUps[0]
						log.Printf("Using fallback prediction: '%s'", g.nextPrediction)
					} else {
						g.nextPrediction = ""
					}
				} else {
					// For longer unknown words, suggest common next words
					g.nextPrediction = "the"
					log.Printf("Using default prediction: '%s'", g.nextPrediction)
				}
			}
		} else if isPartialWord && currentWord != "" {
			// Autocomplete based on word frequency
			lowerCurrent := strings.ToLower(currentWord)
			var bestMatch string
			maxFrequency := 0
			
			// Search for words that start with the current partial word
			for word, freq := range g.wordFrequency {
				if strings.HasPrefix(word, lowerCurrent) && word != lowerCurrent {
					if freq > maxFrequency {
						maxFrequency = freq
						bestMatch = word
					}
				}
			}
			
			if bestMatch != "" {
				g.nextPrediction = bestMatch
				log.Printf("Autocompleting '%s' to '%s' (frequency: %d)", currentWord, bestMatch, maxFrequency)
			} else {
				// No completion found in training data
				g.nextPrediction = ""
				log.Printf("No autocomplete found for '%s'", currentWord)
			}
		}
	} else {
		g.nextPrediction = ""
		log.Printf("No context word available")
	}
}

func (g *Game) Update() error {
	if g.gamepadIDs == nil {
		g.gamepadIDs = map[ebiten.GamepadID]struct{}{}
	}
	
	// Initialize window position on first frame
	if !g.windowInitialized {
		ebiten.SetWindowPosition(int(g.windowX), int(g.windowY))
		g.windowInitialized = true
	}
	
	// Initialize Markov chain
	if g.markovChain == nil {
		g.markovChain = gomarkov.NewChain(1) // Order 1 chain (uses 1 previous word)
		g.wordFrequency = make(map[string]int)
		
		// Load training data from file
		if err := g.loadTrainingData(); err != nil {
			log.Printf("Error loading training data: %v", err)
		}
		
		// If no training data exists, start with some common phrases
		if len(g.trainingData) == 0 {
			g.trainingData = [][]string{
				{"hello", "world"},
				{"how", "are", "you"},
				{"the", "quick", "brown", "fox"},
				{"I", "am", "fine"},
				{"thank", "you", "very", "much"},
				{"what", "is", "your", "name"},
				{"nice", "to", "meet", "you"},
				{"have", "a", "good", "day"},
				{"see", "you", "later"},
				{"good", "morning"},
				{"good", "afternoon"},
				{"good", "evening"},
				{"ok", "thanks"},
				{"ok", "I", "will"},
				{"ok", "let", "me", "check"},
				{"ok", "sounds", "good"},
				{"yes", "I", "agree"},
				{"no", "thank", "you"},
				{"please", "help", "me"},
				{"can", "you", "help"},
				{"this", "is", "great"},
				{"that", "is", "awesome"},
			}
		}
		
		// Train the chain with all saved data
		for _, sentence := range g.trainingData {
			g.markovChain.Add(sentence)
			// Add to word frequency
			for _, word := range sentence {
				g.wordFrequency[strings.ToLower(word)]++
			}
		}
		log.Printf("Loaded %d training sentences", len(g.trainingData))
		
		// Load and train on all previously typed text
		if err := g.loadRawTextAndTrain(); err != nil {
			log.Printf("Error loading raw text: %v", err)
		}
		
		// Generate initial prediction
		g.updatePrediction()
	}

	// Initialize ring keyboard with 2 rings and 2 sets
	if g.rings[0][0] == nil {
		// Main set (Set 0)
		// Inner ring - numbers + common symbols (16 items)
		g.rings[0][0] = []string{
			"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
			".", ",", "-", "_", "⌫", "↵",
		}
		// Outer ring - all letters (26 items)
		g.rings[0][1] = []string{
			"A", "B", "C", "D", "E", "F", "G", "H", "I", "J",
			"K", "L", "M", "N", "O", "P", "Q", "R", "S", "T",
			"U", "V", "W", "X", "Y", "Z",
		}
		
		// Secondary set (Set 1) - coding symbols
		// Inner ring - brackets and special chars (16 items)
		g.rings[1][0] = []string{
			"(", ")", "[", "]", "{", "}", "<", ">", "'", "\"",
			"`", "~", "!", "?", "⌫", "↵",
		}
		// Outer ring - operators and symbols (26 items)
		g.rings[1][1] = []string{
			"+", "-", "*", "/", "=", "!=", "==", "&&", "||", "%",
			"&", "|", "^", "<<", ">>", "@", "#", "$", ":", ";",
			"\\", ".", ",", "_", "->", "=>",
		}
		g.font = basicfont.Face7x13
	}

	// Log the gamepad connection events.
	g.gamepadIDsBuf = inpututil.AppendJustConnectedGamepadIDs(g.gamepadIDsBuf[:0])
	for _, id := range g.gamepadIDsBuf {
		log.Printf("gamepad connected: id: %d, SDL ID: %s", id, ebiten.GamepadSDLID(id))
		g.gamepadIDs[id] = struct{}{}
	}
	for id := range g.gamepadIDs {
		if inpututil.IsGamepadJustDisconnected(id) {
			log.Printf("gamepad disconnected: id: %d", id)
			delete(g.gamepadIDs, id)
		}
	}

	g.axes = map[ebiten.GamepadID][]string{}
	g.pressedButtons = map[ebiten.GamepadID][]string{}
	for id := range g.gamepadIDs {
		maxAxis := ebiten.GamepadAxisType(ebiten.GamepadAxisCount(id))
		for a := range maxAxis {
			v := ebiten.GamepadAxisValue(id, a)
			g.axes[id] = append(g.axes[id], fmt.Sprintf("%d:%+0.2f", a, v))
		}

		maxButton := ebiten.GamepadButton(ebiten.GamepadButtonCount(id))
		for b := ebiten.GamepadButton(0); b < maxButton; b++ {
			if ebiten.IsGamepadButtonPressed(id, b) {
				g.pressedButtons[id] = append(g.pressedButtons[id], strconv.Itoa(int(b)))
			}

			// Log button events.
			if inpututil.IsGamepadButtonJustPressed(id, b) {
				log.Printf("button pressed: id: %d, button: %d", id, b)
			}
			if inpututil.IsGamepadButtonJustReleased(id, b) {
				log.Printf("button released: id: %d, button: %d", id, b)
			}
		}

		if ebiten.IsStandardGamepadLayoutAvailable(id) {
			for b := ebiten.StandardGamepadButton(0); b <= ebiten.StandardGamepadButtonMax; b++ {
				// Log button events.
				if inpututil.IsStandardGamepadButtonJustPressed(id, b) {
					var strong float64
					var weak float64
					switch b {
					case ebiten.StandardGamepadButtonLeftTop,
						ebiten.StandardGamepadButtonLeftLeft,
						ebiten.StandardGamepadButtonLeftRight,
						ebiten.StandardGamepadButtonLeftBottom:
						weak = 0.5
					case ebiten.StandardGamepadButtonRightTop,
						ebiten.StandardGamepadButtonRightLeft,
						ebiten.StandardGamepadButtonRightRight,
						ebiten.StandardGamepadButtonRightBottom:
						strong = 0.5
					}
					if strong > 0 || weak > 0 {
						op := &ebiten.VibrateGamepadOptions{
							Duration:        200 * time.Millisecond,
							StrongMagnitude: strong,
							WeakMagnitude:   weak,
						}
						ebiten.VibrateGamepad(id, op)
					}
					log.Printf("standard button pressed: id: %d, button: %d", id, b)
				}
				if inpututil.IsStandardGamepadButtonJustReleased(id, b) {
					log.Printf("standard button released: id: %d, button: %d", id, b)
				}
			}
		}

		// Handle ring keyboard with left joystick
		if ebiten.IsStandardGamepadLayoutAvailable(id) {
			// Get left stick position
			x := ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisLeftStickHorizontal)
			y := ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisLeftStickVertical)

			// Calculate angle and magnitude
			magnitude := math.Sqrt(x*x + y*y)
			
			// Detect joystick movement
			if magnitude > 0.1 {
				g.lastInputTime = time.Now()
			}

			// Determine which ring based on magnitude
			if magnitude > 0.1 {
				if magnitude < 0.9 {
					g.selectedRing = 0 // Inner ring (90% of range)
				} else {
					g.selectedRing = 1 // Outer ring (last 10%)
				}

				// Calculate angle from joystick position
				// Atan2 gives angle from positive X axis, we need from positive Y axis
				angle := math.Atan2(x, -y) // Note: x and -y are swapped to rotate 90 degrees
				if angle < 0 {
					angle += 2 * math.Pi
				}
				g.joystickAngle = angle

				// Calculate selected character based on angle
				currentRing := g.rings[g.currentSet][g.selectedRing]
				segmentAngle := (2 * math.Pi) / float64(len(currentRing))
				g.selectedIndex = int(angle/segmentAngle) % len(currentRing)
			}

			// Check for any button press
			for b := ebiten.StandardGamepadButton(0); b <= ebiten.StandardGamepadButtonMax; b++ {
				if ebiten.IsStandardGamepadButtonPressed(id, b) {
					g.lastInputTime = time.Now()
					break
				}
			}
			
			// Handle button press to select character
			now := time.Now()
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonRightBottom) {
				// Debounce button presses
				if now.Sub(g.lastButtonTime) > 200*time.Millisecond {
					if magnitude > 0.1 { // Only select if joystick is moved
						// Joystick moved - select from ring
						currentRing := g.rings[g.currentSet][g.selectedRing]
						if g.selectedIndex < len(currentRing) {
							selectedChar := currentRing[g.selectedIndex]
							if selectedChar == "⌫" { // Backspace
								robotgo.KeyTap("backspace")
								// Remove last character from current word
								if len(g.currentSentence) > 0 {
									lastWord := g.currentSentence[len(g.currentSentence)-1]
									if len(lastWord) > 0 {
										g.currentSentence[len(g.currentSentence)-1] = lastWord[:len(lastWord)-1]
										if g.currentSentence[len(g.currentSentence)-1] == "" {
											g.currentSentence = g.currentSentence[:len(g.currentSentence)-1]
										}
									}
								}
								g.updatePrediction()
							} else if selectedChar == "↵" { // Enter
								robotgo.KeyTap("enter")
								// Save newline to raw text
								if err := g.appendToRawText("\n"); err != nil {
									log.Printf("Error saving newline: %v", err)
								}
								// Train markov chain with current sentence if it has words
								if len(g.currentSentence) > 1 {
									g.markovChain.Add(g.currentSentence)
									g.trainingData = append(g.trainingData, append([]string{}, g.currentSentence...))
									// Update word frequency
									for _, word := range g.currentSentence {
										if word != "" {
											g.wordFrequency[strings.ToLower(word)]++
										}
									}
									if err := g.saveTrainingData(); err != nil {
										log.Printf("Error saving training data: %v", err)
									}
								}
								g.currentSentence = []string{}
							} else {
								// Apply uppercase/lowercase transformation for letters
								outputChar := selectedChar
								if len(selectedChar) == 1 && selectedChar >= "A" && selectedChar <= "Z" {
									if g.uppercase {
										robotgo.TypeStr(selectedChar)
									} else {
										outputChar = strings.ToLower(selectedChar)
										robotgo.TypeStr(outputChar)
									}
								} else {
									robotgo.TypeStr(selectedChar)
								}
								
								// Save typed character to raw text file
								if err := g.appendToRawText(outputChar); err != nil {
									log.Printf("Error saving typed text: %v", err)
								}
								
								// Track the character for word building
								if len(g.currentSentence) == 0 {
									g.currentSentence = []string{""}
								}
								g.currentSentence[len(g.currentSentence)-1] += outputChar
								log.Printf("Added char '%s' to word. Current sentence: %v", outputChar, g.currentSentence)
								g.updatePrediction()
							}
							g.lastButtonTime = now
						}
					}
				}
			}


			// Delete one character with B button (RightRight)
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonRightRight) {
				robotgo.KeyTap("backspace")
				// Handle backspace for word tracking
				if len(g.currentSentence) > 0 {
					lastWord := g.currentSentence[len(g.currentSentence)-1]
					if len(lastWord) > 0 {
						g.currentSentence[len(g.currentSentence)-1] = lastWord[:len(lastWord)-1]
						if g.currentSentence[len(g.currentSentence)-1] == "" {
							g.currentSentence = g.currentSentence[:len(g.currentSentence)-1]
						}
					}
				}
				g.updatePrediction()
			}

			// Add space with X button (RightLeft)
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonRightLeft) {
				robotgo.TypeStr(" ")
				// Save space to raw text
				if err := g.appendToRawText(" "); err != nil {
					log.Printf("Error saving space: %v", err)
				}
				// Start a new word
				if len(g.currentSentence) > 0 && g.currentSentence[len(g.currentSentence)-1] != "" {
					g.currentSentence = append(g.currentSentence, "")
					log.Printf("Space pressed - new word started. Sentence: %v", g.currentSentence)
				} else if len(g.currentSentence) == 0 {
					// Start with empty word if no sentence yet
					g.currentSentence = []string{""}
				}
				g.updatePrediction()
			}
			
			// Add new line with Y button (RightTop)
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonRightTop) {
				robotgo.KeyTap("enter")
				// Save newline to raw text
				if err := g.appendToRawText("\n"); err != nil {
					log.Printf("Error saving newline: %v", err)
				}
				// Train markov chain with current sentence if it has words
				if len(g.currentSentence) > 1 {
					g.markovChain.Add(g.currentSentence)
					g.trainingData = append(g.trainingData, append([]string{}, g.currentSentence...))
					// Update word frequency
					for _, word := range g.currentSentence {
						if word != "" {
							g.wordFrequency[strings.ToLower(word)]++
						}
					}
					if err := g.saveTrainingData(); err != nil {
						log.Printf("Error saving training data: %v", err)
					}
				}
				g.currentSentence = []string{}
			}
			
			// D-pad arrow key mapping
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonLeftTop) {
				robotgo.KeyTap("up")
			}
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonLeftBottom) {
				robotgo.KeyTap("down")
			}
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonLeftLeft) {
				robotgo.KeyTap("left")
			}
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonLeftRight) {
				robotgo.KeyTap("right")
			}

			// Hold L1/button 4 to show secondary character set
			if ebiten.IsGamepadButtonPressed(id, ebiten.GamepadButton(4)) {
				g.currentSet = 1 // Show secondary set while held
			} else {
				g.currentSet = 0 // Return to main set when released
			}
			
			// Hold R1/button 5 for uppercase
			if ebiten.IsGamepadButtonPressed(id, ebiten.GamepadButton(5)) {
				g.uppercase = true // Uppercase while held
			} else {
				g.uppercase = false // Lowercase when released
			}
			
			// R2/button 7 for accepting word prediction
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButton(7)) {
				log.Printf("R2 (button 7) pressed, prediction: '%s'", g.nextPrediction)
				if g.nextPrediction != "" {
					// Determine what to type based on current word state
					var toType string
					var currentWord string
					
					if len(g.currentSentence) > 0 && g.currentSentence[len(g.currentSentence)-1] != "" {
						// We have a partial word - only type the completion
						currentWord = g.currentSentence[len(g.currentSentence)-1]
						if strings.HasPrefix(strings.ToLower(g.nextPrediction), strings.ToLower(currentWord)) {
							// Prediction starts with current word, type only the rest
							toType = g.nextPrediction[len(currentWord):] + " "
						} else {
							// Prediction doesn't match, replace the whole word
							// First delete the current partial word
							for i := 0; i < len(currentWord); i++ {
								robotgo.KeyTap("backspace")
							}
							toType = g.nextPrediction + " "
						}
					} else {
						// No partial word, type the whole prediction
						toType = g.nextPrediction + " "
					}
					
					// Type the completion
					robotgo.TypeStr(toType)
					
					// Save what was actually typed to raw text
					if err := g.appendToRawText(toType); err != nil {
						log.Printf("Error saving predicted word: %v", err)
					}
					
					// Update sentence tracking with the complete word
					if len(g.currentSentence) == 0 {
						g.currentSentence = []string{g.nextPrediction, ""}
					} else if g.currentSentence[len(g.currentSentence)-1] == "" {
						g.currentSentence[len(g.currentSentence)-1] = g.nextPrediction
						g.currentSentence = append(g.currentSentence, "")
					} else {
						// Update with the complete word
						g.currentSentence[len(g.currentSentence)-1] = g.nextPrediction
						g.currentSentence = append(g.currentSentence, "")
					}
					log.Printf("After prediction applied. Sentence: %v", g.currentSentence)
					g.updatePrediction()
				}
			}
			
			// Handle right joystick for window movement
			rightX := ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisRightStickHorizontal)
			rightY := ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisRightStickVertical)
			
			// Apply dead zone
			if math.Abs(rightX) > 0.1 || math.Abs(rightY) > 0.1 {
				// Movement speed in pixels per frame
				moveSpeed := 25.0
				
				// Calculate new position
				newX := g.windowX + rightX * moveSpeed
				newY := g.windowY + rightY * moveSpeed
				
				// Get monitor bounds (we'll use the monitor work area)
				monitorX, monitorY := ebiten.Monitor().Size()
				
				// Clamp to screen boundaries
				if newX < 0 {
					newX = 0
				}
				if newY < 0 {
					newY = 0
				}
				if newX > float64(monitorX - screenWidth) {
					newX = float64(monitorX - screenWidth)
				}
				if newY > float64(monitorY - screenHeight) {
					newY = float64(monitorY - screenHeight)
				}
				
				// Update window position
				g.windowX = newX
				g.windowY = newY
				ebiten.SetWindowPosition(int(g.windowX), int(g.windowY))
				
				// Mark as having input
				g.lastInputTime = time.Now()
			}
			
			// Button 9 to toggle visibility
			if inpututil.IsGamepadButtonJustPressed(id, ebiten.GamepadButton(9)) {
				g.isVisible = !g.isVisible
				log.Printf("Visibility toggled: %v", g.isVisible)
			}
		}
	}
	
	// Update opacity based on visibility toggle
	if g.isVisible {
		g.opacity = 1.0
	} else {
		g.opacity = 0.0
	}
	
	return nil
}

func (g *Game) applyOpacity(c color.RGBA) color.RGBA {
	c.A = uint8(float64(c.A) * g.opacity)
	return c
}

func (g *Game) Draw(screen *ebiten.Image) {
	// Clear screen with transparent background
	screen.Fill(color.RGBA{0, 0, 0, 0})
	
	// Don't draw anything if fully invisible
	if g.opacity <= 0 {
		return
	}
	
	// Draw ring keyboard
	centerX := float64(screenWidth / 2)
	centerY := float64(screenHeight / 2)

	if len(g.gamepadIDs) > 0 {
		// Get current joystick magnitude first
		var currentMagnitude float64
		for id := range g.gamepadIDs {
			if ebiten.IsStandardGamepadLayoutAvailable(id) {
				x := ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisLeftStickHorizontal)
				y := ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisLeftStickVertical)
				currentMagnitude = math.Sqrt(x*x + y*y)
				break
			}
		}

		// Define radii for the 2 rings
		radii := [2]float64{120, 200}

		// Draw all 2 rings - from outer to inner to prevent overlap
		for ringIdx := 1; ringIdx >= 0; ringIdx-- {
			ring := g.rings[g.currentSet][ringIdx]
			radius := radii[ringIdx]


			// Draw characters in this ring
			for i, char := range ring {
				// Start from top (12 o'clock) by subtracting Pi/2
				angle := float64(i)*(2*math.Pi)/float64(len(ring)) - math.Pi/2
				x := centerX + radius*math.Cos(angle)
				y := centerY + radius*math.Sin(angle)

				// Highlight selected character in active ring
				textColor := g.applyOpacity(color.RGBA{150, 150, 150, 255})              // Dimmer for inactive rings
				if currentMagnitude > 0.1 && ringIdx == g.selectedRing { // Only highlight if joystick is moved
					textColor = g.applyOpacity(color.RGBA{255, 255, 255, 255})
					if i == g.selectedIndex {
						textColor = g.applyOpacity(color.RGBA{0, 255, 255, 255}) // Cyan for selected
						// Draw selection indicator
						ebitenutil.DrawCircle(screen, x, y, 20, g.applyOpacity(color.RGBA{0, 255, 255, 64}))
					}
				}

				// Draw character with background circle for visibility
				// Different color for each ring
				bgColors := [2]color.RGBA{
					{80, 40, 40, 255}, // Dark red for inner
					{40, 40, 80, 255}, // Dark blue for outer
				}
				bgColor := bgColors[ringIdx]
				if currentMagnitude > 0.1 && ringIdx == g.selectedRing { // Only brighten if joystick is moved
					bgColor.R += 50
					bgColor.G += 50
					bgColor.B += 50
				}
				ebitenutil.DrawCircle(screen, x, y, 18, g.applyOpacity(bgColor))

				// Draw character with case transformation
				displayChar := char
				if len(char) == 1 && char >= "A" && char <= "Z" {
					if !g.uppercase {
						displayChar = strings.ToLower(char)
					}
				}
				bounds := text.BoundString(g.font, displayChar)
				textX := int(x) - bounds.Dx()/2
				textY := int(y) + bounds.Dy()/2
				text.Draw(screen, displayChar, g.font, textX, textY, textColor)
			}
		}

		// Draw predicted word in the center
		if g.nextPrediction != "" {
			// Create a background for better visibility
			bgColor := g.applyOpacity(color.RGBA{40, 40, 40, 200})
			predBounds := text.BoundString(g.font, g.nextPrediction)
			padding := 10
			bgX := int(centerX) - predBounds.Dx()/2 - padding
			bgY := int(centerY) - predBounds.Dy()/2 - padding
			bgW := predBounds.Dx() + padding*2
			bgH := predBounds.Dy() + padding*2
			
			// Draw background rectangle
			for y := bgY; y < bgY+bgH; y++ {
				for x := bgX; x < bgX+bgW; x++ {
					screen.Set(x, y, bgColor)
				}
			}
			
			// Draw the predicted word
			predX := int(centerX) - predBounds.Dx()/2
			predY := int(centerY) + predBounds.Dy()/2
			text.Draw(screen, g.nextPrediction, g.font, predX, predY, g.applyOpacity(color.RGBA{0, 255, 0, 255}))
		}

	} else {
		str := "Please connect your gamepad."
		bounds := text.BoundString(g.font, str)
		text.Draw(screen, str, g.font, (screenWidth-bounds.Dx())/2, screenHeight/2, g.applyOpacity(color.RGBA{255, 255, 255, 255}))
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

func main() {
	ebiten.SetWindowSize(screenWidth, screenHeight)
	ebiten.SetWindowTitle("Ring Keyboard Controller - 2 Rings with L1 Toggle")
	ebiten.SetWindowDecorated(false)
	ebiten.SetScreenTransparent(true)
	ebiten.SetWindowFloating(true)
	ebiten.SetWindowMousePassthrough(true)
	
	// Initialize game with center screen position
	game := &Game{
		windowX: 100.0,  // Default starting position
		windowY: 100.0,
		isVisible: true, // Start visible
	}
	
	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
