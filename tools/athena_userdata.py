"""Correct the pinned Athena installer's nested persistent-data cleanup.

The platform mounts userdata/techo5 at /data in normal Linux. Preserve setup
but remove old app overrides there, not just at the older userdata roots.
Only the exact reviewed function from Echo Linux af8de59 is accepted; all
other installer code is kept byte-for-byte. No device operations occur here.
"""
import ast
import hashlib

ORIGINAL_SHA256 = '3c87aacd8e83148e70a66e42e6611e10cf24cbfed93b062fd34e30c9e35c3dfd'
PATCHED_SHA256 = '19622370bc2aa3ca8188417a59f04e9e4c897a9b5d9a59efe68ad8ce5e571fa1'


def patch_source(source: str) -> str:
    try:
        functions = [node for node in ast.parse(source).body
                     if isinstance(node, ast.FunctionDef) and node.name == 'prepare_tater_userdata']
    except SyntaxError as error:
        raise ValueError('invalid Athena installer source') from error
    if len(functions) != 1:
        raise ValueError('Athena installer must contain exactly one userdata cleanup function')
    original = ast.get_source_segment(source, functions[0])
    identity = hashlib.sha256(original.encode()).hexdigest()
    if identity == PATCHED_SHA256:
        return source
    if identity != ORIGINAL_SHA256 or source.count(original) != 1:
        raise ValueError('Athena userdata cleanup differs from the reviewed pinned installer')
    patched = original.replace(
        "            ud + '/techo5/techo5-linux',",
        "            ud + '/techo5/techo5-linux',\n"
        "            ud + '/techo5/local/etc/tater',\n"
        "            ud + '/techo5/local/share/tater',\n"
        "            ud + '/techo5/tater-linux',")
    patched = patched.replace(
        "            ud + '/techo5/techo5-linux/app.new',",
        "            ud + '/techo5/techo5-linux/app.new',\n"
        "            ud + '/techo5/tater-linux/app',\n"
        "            ud + '/techo5/tater-linux/app.new',")
    if hashlib.sha256(patched.encode()).hexdigest() != PATCHED_SHA256:
        raise ValueError('Athena userdata correction failed its exact source check')
    return source.replace(original, patched, 1)
