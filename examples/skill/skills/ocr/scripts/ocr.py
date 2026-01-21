#!/usr/bin/env python3
"""
OCR Script for Image Text Extraction
Extract text from images using Tesseract OCR
"""

import argparse
import json
import sys
import os

try:
    import pytesseract
    from PIL import Image, ImageEnhance, ImageFilter
except ImportError as e:
    print(f"Error: Required package not installed: {e}", file=sys.stderr)
    print("Run: pip install pytesseract Pillow", file=sys.stderr)
    sys.exit(1)


def preprocess_image(image):
    """Apply preprocessing to improve OCR accuracy"""
    # Convert to grayscale
    image = image.convert('L')
    
    # Enhance contrast
    enhancer = ImageEnhance.Contrast(image)
    image = enhancer.enhance(2.0)
    
    # Apply sharpening
    image = image.filter(ImageFilter.SHARPEN)
    
    return image


def ocr_image(image_path, output_path, lang="eng", preprocess=False, output_format="text"):
    """Extract text from image using OCR"""
    
    # Validate input file
    if not os.path.exists(image_path):
        print(f"Error: Image file not found: {image_path}", file=sys.stderr)
        sys.exit(1)
    
    try:
        # Load image
        print(f"Loading image: {image_path}...", file=sys.stderr)
        image = Image.open(image_path)
        
        # Preprocess if requested
        if preprocess:
            print("Applying image preprocessing...", file=sys.stderr)
            image = preprocess_image(image)
        
        # Perform OCR
        print(f"Extracting text (language: {lang})...", file=sys.stderr)
        
        if output_format == "json":
            # Get detailed data with confidence scores
            data = pytesseract.image_to_data(image, lang=lang, output_type=pytesseract.Output.DICT)
            
            # Extract text and confidence
            text_parts = []
            for i, text in enumerate(data['text']):
                if text.strip():
                    text_parts.append(text)
            
            full_text = " ".join(text_parts)
            avg_conf = sum(c for c in data['conf'] if c != -1) / max(len([c for c in data['conf'] if c != -1]), 1)
            
            output_data = {
                "text": full_text.strip(),
                "language": lang,
                "confidence": round(avg_conf, 2),
                "image_path": image_path
            }
            
            with open(output_path, "w", encoding="utf-8") as f:
                json.dump(output_data, f, ensure_ascii=False, indent=2)
        else:
            # Plain text output
            text = pytesseract.image_to_string(image, lang=lang)
            
            with open(output_path, "w", encoding="utf-8") as f:
                f.write(text.strip())
        
        print(f"✓ Text extracted successfully", file=sys.stderr)
        print(f"  Output saved to: {output_path}", file=sys.stderr)
        
    except Exception as e:
        print(f"Error during OCR processing: {e}", file=sys.stderr)
        sys.exit(1)


def main():
    parser = argparse.ArgumentParser(description="Extract text from images using OCR")
    parser.add_argument("image_file", help="Input image file path")
    parser.add_argument("output_file", help="Output text/JSON file path")
    parser.add_argument("--lang", default="eng",
                       help="Language code (e.g., eng, chi_sim, jpn). Default: eng")
    parser.add_argument("--preprocess", action="store_true",
                       help="Apply image preprocessing for better accuracy")
    parser.add_argument("--format", default="text", choices=["text", "json"],
                       help="Output format (default: text)")
    
    args = parser.parse_args()
    
    ocr_image(
        args.image_file,
        args.output_file,
        lang=args.lang,
        preprocess=args.preprocess,
        output_format=args.format
    )


if __name__ == "__main__":
    main()
