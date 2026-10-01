"""Prompting and answer normalisation for the final detection."""

import json
import re

KEYWORDS = {
    "trash": ("trash", "garbage", "litter", "rubbish", "bottle", "cup", "wrapper", "plastic bag", "tissue"),
    "spill": ("spill", "liquid", "puddle", "wet floor", "water on", "stain"),
    "floor_damage": ("crack", "broken tile", "damaged floor", "hole", "loose tile", "chipped"),
    "fallen_person": ("fallen", "lying on the floor", "person lying", "collapsed", "unconscious"),
}

PROMPT = (
    "You are the final reviewer of a mall patrol robot. The robot flagged this photo as "
    '"{early}" ({confidence:.0%} sure). Look carefully at the floor and the people. '
    'Decide which single category is really present: {options}. Use "none" when the robot was wrong. '
    'Answer only JSON: {{"category": "<one of {options}>", "confidence": <0..1>, '
    '"description": "<one sentence in {language} describing what and where>"}}.'
)


def build_prompt(
    categories: list[str], early: str, early_confidence: float, language: str = "English"
) -> str:
    """Return the review prompt."""
    options = ", ".join([*categories, "none"])
    return PROMPT.format(
        early=early.replace("_", " "), confidence=early_confidence, options=options, language=language
    )


def keyword_category(text: str, categories: list[str]) -> str:
    """Map free text to a category, 'none' when nothing matches."""
    low = text.lower()
    for category in categories:
        if any(word in low for word in KEYWORDS.get(category, (category.replace("_", " "),))):
            return category
    return "none"


def normalise(answer: str, categories: list[str]) -> dict:
    """Turn a model answer into {category, confidence, description}."""
    data: dict = {}
    match = re.search(r"\{.*\}", answer, re.S)
    if match:
        try:
            data = json.loads(match.group(0))
        except ValueError:
            data = {}
    raw = str(data.get("category", "")).strip().lower().replace(" ", "_")
    description = str(data.get("description") or answer).strip()[:400]
    if raw in categories or raw == "none":
        category = raw
    else:
        category = keyword_category(f"{raw} {description}", categories)
    try:
        confidence = float(data.get("confidence", 0.6))
    except (TypeError, ValueError):
        confidence = 0.6
    if confidence > 1.0:
        confidence /= 100.0
    return {
        "category": category,
        "confidence": round(min(1.0, max(0.0, confidence)), 3),
        "description": description,
    }
