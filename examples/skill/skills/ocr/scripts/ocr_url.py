#!/usr/bin/env python3
"""
OCR Script for Remote Images
Download and extract text from images hosted on the web
"""

import argparse
import sys
import os
import tempfile

try:
    import pytesseract
    from PIL import Image, ImageEnhance, ImageFilter
    import requests
except ImportError as e:
    print(f"Error: Required package not installed: {e}", file=sys.stderr)
    print("Run: pip install pytesseract Pillow requests", file=sys.stderr)
    sys.exit(1)


def preprocess_image(image):
    """Apply preprocessing to improve OCR accuracy"""
    image = image.convert('L')
    enhancer = ImageEnhance.Contrast(image)
    image = enhancer.enhance(2.0)
    image = image.filter(ImageFilter.SHARPEN)
    return image


def ocr_from_url(image_url, output_path, lang="eng", preprocess=False, output_format="text"):
    """Download image from URL and extract text using OCR"""
    
    try:
        # Download image
        print(f"Downloading image from: {image_url}...", file=sys.stderr)
        response = requests.get(image_url, timeout=30, stream=True)
        response.raise_for_status()
        
        # Save to temporary file
        with tempfile.NamedTemporaryFile(delete=False, suffix=".img") as tmp_file:
            for chunk in response.iter_content(chunk_size=8192):
                tmp_file.write(chunk)
            tmp_path = tmp_file.name
        
        print(f"Image downloaded to: {tmp_path}", file=sys.stderr)
        
        # Load image
        image = Image.open(tmp_path)
        
        # Preprocess if requested
        if preprocess:
            print("Applying image preprocessing...", file=sys.stderr)
            image = preprocess_image(image)
        
        # Perform OCR
        print(f"Extracting text (language: {lang})...", file=sys.stderr)
        text = pytesseract.image_to_string(image, lang=lang)
        
        # Write output
        with open(output_path, "w", encoding="utf-8") as f:
            f.write(text.strip())
        
        # Clean up temp file
        os.unlink(tmp_path)
        
        print(f"✓ Text extracted successfully", file=sys.stderr)
        print(f"  Output saved to: {output_path}", file=sys.stderr)
        
    except requests.RequestException as e:
        print(f"Error downloading image: {e}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"Error during OCR processing: {e}", file=sys.stderr)
        sys.exit(1)


def main():
    parser = argparse.ArgumentParser(description="Extract text from remote images using OCR")
    parser.add_argument("image_url", help="Image URL")
    parser.add_argument("output_file", help="Output text file path")
    parser.add_argument("--lang", default="eng",
                       help="Language code (e.g., eng, chi_sim, jpn). Default: eng")
    parser.add_argument("--preprocess", action="store_true",
                       help="Apply image preprocessing for better accuracy")
    parser.add_argument("--format", default="text", choices=["text", "json"],
                       help="Output format (default: text)")
    
    args = parser.parse_args()
    
    ocr_from_url(
        args.image_url,
        args.output_file,
        lang=args.lang,
        preprocess=args.preprocess,
        output_format=args.format
    )


if __name__ == "__main__":
    main()
