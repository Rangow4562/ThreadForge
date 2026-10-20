# Synthetic thermal qualification policy

The qualification ambient is 45 C. For this exercise, subtract 2 C from the
module maximum case-temperature rating for every 1 C of ambient above the
25 C reference point:

`derated_limit = maximum_case_rating - 2 * (ambient - 25)`.

Compare the resulting limit with the logged case temperatures.
