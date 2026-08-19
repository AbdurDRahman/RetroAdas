# Hardware Bill of Materials (BOM)

## 1. Core Compute & Storage
The brains of the system. The 8GB model is strictly required to prevent memory crashes when running the zero-copy pipeline and TensorRT engines simultaneously.

| Component | Specification / Purpose | Est. Price (PKR) | Est. Price (USD) |
| :--- | :--- | :--- | :--- |
| NVIDIA Jetson Orin Nano Developer Kit | 8GB LPDDR5 Version. Core AI processor. Runs YOLOv8n + DepthAnything V2 on Ampere Tensor Cores. | ₨ 150,000 | ~$535 |
| 128GB NVMe M.2 SSD | High-speed storage for JetPack 6.0 OS, swap space, and DeepStream frameworks (do not use MicroSD). | ₨ 5,500 | ~$20 |
| Active Cooling Fan | Usually included with the dev kit, but a dedicated PWM heat-sink fan is required for the 50°C car cabin. | ₨ 1,500 | ~$5 |
| **Subtotal** | | **₨ 157,000** | **~$560** |

## 2. Vision Sensors (The Hybrid 4-Camera Setup)
This setup avoids USB saturation and solves the physical MIPI CSI distance limits from the front windshield to the rear bumper.

| Component | Specification / Purpose | Est. Price (PKR) | Est. Price (USD) |
| :--- | :--- | :--- | :--- |
| Front Camera: IMX219 (8MP) | 30cm CSI ribbon cable. Plugs directly into Jetson CSI Port 0 for hardware ISP processing. | ₨ 8,500 | ~$30 |
| Rear Camera: IMX219 (8MP) | Mounts on rear bumper. Connects to the Arducam Tx board. | ₨ 8,500 | ~$30 |
| Arducam LAN Extender Kit | SKU: U6279. Bridges the rear IMX219 to Jetson CSI Port 1 over an ethernet cable. | ₨ 15,000 | ~$54 |
| Cat5e / Cat6 Cable | 3 to 5 meters. Carries the MIPI signal from the rear bumper to the dashboard. | ₨ 500 | ~$2 |
| Left Blindspot Camera | Generic USB 3.0 UVC Web Camera (720p/1080p). Connects to Jetson USB-A Root Hub 1. | ₨ 5,000 | ~$18 |
| Right Blindspot Camera | Generic USB 3.0 UVC Web Camera (720p/1080p). Connects to Jetson USB-C Root Hub 2. | ₨ 5,000 | ~$18 |
| **Subtotal** | | **₨ 42,500** | **~$152** |

## 3. Radar Sensors (Sensor Fusion & Sentry Mode)
These components answer the panel's critique regarding vision-only limitations and provide ultra-low power parking protection.

| Component | Specification / Purpose | Est. Price (PKR) | Est. Price (USD) |
| :--- | :--- | :--- | :--- |
| 2x HLK-LD2451 24GHz Radars | Fused with the rear/side cameras for Blind Spot and Rear Collision Warnings. Outputs UART data. | ₨ 5,000 | ~$18 |
| 1x RCWL-0516 Microwave Radar | 10.525 GHz Doppler sensor. Always-on sentry trigger to wake the Jetson from SC7 Deep Sleep. | ₨ 300 | ~$1 |
| USB-to-TTL UART Converters | (CP2102 or CH340). Interfaces the LD2451 radars directly into the Jetson's USB ports if GPIO UART is full. | ₨ 800 | ~$3 |
| **Subtotal** | | **₨ 6,100** | **~$22** |

## 4. Power Electronics & Wake Management
Standard components will fry the Jetson during a car's engine ignition. Automotive-grade regulation is required.

| Component | Specification / Purpose | Est. Price (PKR) | Est. Price (USD) |
| :--- | :--- | :--- | :--- |
| Automotive DC-DC Buck Converter | 12V/24V to 5V (Minimum 5A/25W output). Must have transient voltage/load-dump protection. | ₨ 3,500 | ~$12 |
| 20,000mAh Power Bank | Failsafe battery for Sentry Mode. Powers the Jetson at 0.4W for up to 6.5 days without draining the car. | ₨ 8,500 | ~$30 |
| MOSFET / Relay Module | Cuts 5V power to the USB cameras when the system enters SC7 sleep to stop battery drain. | ₨ 500 | ~$2 |
| **Subtotal** | | **₨ 12,500** | **~$44** |

## 5. User Interface & Hardware Peripherals
The in-cabin driver warning system.

| Component | Specification / Purpose | Est. Price (PKR) | Est. Price (USD) |
| :--- | :--- | :--- | :--- |
| 5-inch or 7-inch HDMI IPS LCD | Generic Waveshare-style bare IPS screen for dashboard mounting. Connects via Jetson DisplayPort/HDMI. | ₨ 13,000 | ~$46 |
| Piezo Buzzer Module | Connects to Jetson GPIO to provide instant audio beeps for Time-To-Collision threshold breaches. | ₨ 100 | ~$0.50 |
| Enclosure, Wiring & Mounts | 3D-printed housings for cameras, dashboard mounting tape, wire shielding, heat shrink tubing, and zip ties. | ₨ 3,000 | ~$11 |
| **Subtotal** | | **₨ 16,100** | **~$57.50** |

## 📊 Total Project Budget Summary

| Category | PKR Total | USD Total |
| :--- | :--- | :--- |
| 1. Core Compute & Storage | ₨ 157,000 | ~$560 |
| 2. Vision Sensors | ₨ 42,500 | ~$152 |
| 3. Radar Sensors | ₨ 6,100 | ~$22 |
| 4. Power Electronics | ₨ 12,500 | ~$44 |
| 5. User Interface & Hardware | ₨ 16,100 | ~$57.50 |
| **Grand Total** | **~₨ 234,200** | **~$835.50** |

*(Note: Prices can fluctuate slightly based on import duties, shipping fees, and currency conversion rates at the time of purchase.)*
