package main

import (
	"fmt"
	"image/color"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/go-vgo/robotgo"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
)

const (
	screenWidth  = 800
	screenHeight = 600
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
}

func (g *Game) Update() error {
	if g.gamepadIDs == nil {
		g.gamepadIDs = map[ebiten.GamepadID]struct{}{}
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

	// Track if there's any gamepad input
	hasInput := false
	
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
				hasInput = true
				g.lastInputTime = time.Now()
			}

			// Determine which ring based on magnitude
			if magnitude > 0.1 {
				if magnitude < 0.95 {
					g.selectedRing = 0 // Inner ring (95% of range)
				} else {
					g.selectedRing = 1 // Outer ring (last 5%)
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
					hasInput = true
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
							} else if selectedChar == "↵" { // Enter
								robotgo.KeyTap("enter")
							} else {
								// Apply uppercase/lowercase transformation for letters
								if len(selectedChar) == 1 && selectedChar >= "A" && selectedChar <= "Z" {
									if g.uppercase {
										robotgo.TypeStr(selectedChar)
									} else {
										robotgo.TypeStr(strings.ToLower(selectedChar))
									}
								} else {
									robotgo.TypeStr(selectedChar)
								}
							}
							g.lastButtonTime = now
						}
					}
				}
			}


			// Delete one character with B button (RightRight)
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonRightRight) {
				robotgo.KeyTap("backspace")
			}

			// Add space with X button (RightLeft)
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonRightLeft) {
				robotgo.TypeStr(" ")
			}
			
			// Add new line with Y button (RightTop)
			if inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonRightTop) {
				robotgo.KeyTap("enter")
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
		}
	}
	
	// Update opacity based on input activity
	if hasInput {
		g.opacity = 1.0 // Full opacity when there's input
	} else {
		g.opacity = 0.0 // Immediately invisible when no input
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
	if err := ebiten.RunGame(&Game{}); err != nil {
		log.Fatal(err)
	}
}
