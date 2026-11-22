#!/usr/bin/env python3
"""
Updates Helm RBAC templates from config/rbac manifests.
"""

import os
import re
import sys
import yaml


def load_rules_from_manifest(path, kind=None, name=None):
    with open(path, "r", encoding="utf-8") as f:
        docs = yaml.safe_load_all(f)
        for doc in docs:
            if not doc:
                continue
            if kind and doc.get("kind") != kind:
                continue
            if name and doc.get("metadata", {}).get("name") != name:
                continue
            if "rules" in doc:
                return doc["rules"]
    return None


def format_rules_yaml(rules):
    dumped = yaml.dump(rules, sort_keys=False)
    # Ensure clean YAML lines
    return dumped.strip() + "\n"


def update_clusterrole():
    src = "config/rbac/role.yaml"
    dst = "charts/traffic-operator/templates/clusterrole.yaml"
    rules = load_rules_from_manifest(src, kind="ClusterRole")
    if not rules:
        print(f"Error: No rules found in {src}", file=sys.stderr)
        return False

    with open(dst, "r", encoding="utf-8") as f:
        content = f.read()

    # Split header (up to and including 'rules:') and footer (starting with '{{- if .Values.rbac.metricsAuth.enabled }}')
    header_match = re.search(r"^(.*?^rules:\n)", content, re.MULTILINE | re.DOTALL)
    footer_match = re.search(r"(\{\{- if \.Values\.rbac\.metricsAuth\.enabled \}\}.*)$", content, re.MULTILINE | re.DOTALL)

    if not header_match or not footer_match:
        print(f"Error: Could not locate rules section in {dst}", file=sys.stderr)
        return False

    header = header_match.group(1)
    footer = footer_match.group(1)

    new_content = header + format_rules_yaml(rules) + footer
    with open(dst, "w", encoding="utf-8") as f:
        f.write(new_content)

    print(f"Updated {dst} from {src}")
    return True


def update_leader_election_role():
    src = "config/rbac/leader_election_role.yaml"
    dst = "charts/traffic-operator/templates/role.yaml"
    rules = load_rules_from_manifest(src, kind="Role")
    if not rules:
        print(f"Error: No rules found in {src}", file=sys.stderr)
        return False

    with open(dst, "r", encoding="utf-8") as f:
        content = f.read()

    header_match = re.search(r"^(.*?^rules:\n)", content, re.MULTILINE | re.DOTALL)
    footer_match = re.search(r"(\{\{- end \}\}\s*)$", content, re.MULTILINE | re.DOTALL)

    if not header_match or not footer_match:
        print(f"Error: Could not locate rules section in {dst}", file=sys.stderr)
        return False

    header = header_match.group(1)
    footer = footer_match.group(1)

    new_content = header + format_rules_yaml(rules) + footer
    with open(dst, "w", encoding="utf-8") as f:
        f.write(new_content)

    print(f"Updated {dst} from {src}")
    return True


def update_user_roles():
    admin_rules = load_rules_from_manifest("config/rbac/trafficcheck_admin_role.yaml", kind="ClusterRole")
    editor_rules = load_rules_from_manifest("config/rbac/trafficcheck_editor_role.yaml", kind="ClusterRole")
    viewer_rules = load_rules_from_manifest("config/rbac/trafficcheck_viewer_role.yaml", kind="ClusterRole")

    dst = "charts/traffic-operator/templates/user-roles.yaml"
    with open(dst, "r", encoding="utf-8") as f:
        content = f.read()

    # user-roles.yaml has 3 documents separated by ---
    # We update rules in each section
    pattern = (
        r"(name:\s*\{\{ include \"traffic-operator\.fullname\" \. \}\}-trafficcheck-admin.*?\nrules:\n).*?"
        r"(---\s*\n.*?name:\s*\{\{ include \"traffic-operator\.fullname\" \. \}\}-trafficcheck-editor.*?\nrules:\n).*?"
        r"(---\s*\n.*?name:\s*\{\{ include \"traffic-operator\.fullname\" \. \}\}-trafficcheck-viewer.*?\nrules:\n).*?"
        r"(\{\{- end \}\}\s*)$"
    )
    match = re.search(pattern, content, re.DOTALL)
    if not match:
        print(f"Error: Could not match user-roles structure in {dst}", file=sys.stderr)
        return False

    # Extract prefix up to admin rules
    prefix_match = re.search(
        r"^(.*?" + re.escape(match.group(1)) + r")", content, re.DOTALL
    )
    if not prefix_match:
        return False

    part1 = match.group(1)
    part2 = match.group(2)
    part3 = match.group(3)
    part4 = match.group(4)

    start_idx = content.find(part1)
    header = content[:start_idx]

    new_content = (
        header
        + part1
        + format_rules_yaml(admin_rules)
        + part2
        + format_rules_yaml(editor_rules)
        + part3
        + format_rules_yaml(viewer_rules)
        + part4
    )

    with open(dst, "w", encoding="utf-8") as f:
        f.write(new_content)

    print(f"Updated {dst} from config/rbac/trafficcheck_*_role.yaml")
    return True


def main():
    base_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
    os.chdir(base_dir)

    success = True
    if not update_clusterrole():
        success = False
    if not update_leader_election_role():
        success = False
    if not update_user_roles():
        success = False

    if not success:
        sys.exit(1)
    print("All Helm RBAC templates successfully updated from config/rbac.")


if __name__ == "__main__":
    main()
